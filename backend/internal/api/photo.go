package api

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"net/http"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
	"github.com/koreesmith/dapper/backend/internal/session"
)

// Profile photos are stored in the jpegPhoto attribute. Uploads are checked to be real JPEG images,
// limited in size, and re-encoded so nothing but picture data is stored.
const (
	maxPhotoUpload = 2 << 20 // bytes accepted from the browser
	maxPhotoSide   = 4096    // pixels; larger pictures are refused rather than decoded
	photoField     = "jpegPhoto"
)

// cleanPhoto validates an uploaded JPEG and returns a re-encoded copy.
func cleanPhoto(b []byte) ([]byte, error) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 || b[2] != 0xFF {
		return nil, validationError("That isn't a JPEG picture. Choose a .jpg or .jpeg file.")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, validationError("That picture couldn't be read. Try a different file.")
	}
	if cfg.Width > maxPhotoSide || cfg.Height > maxPhotoSide || cfg.Width < 1 || cfg.Height < 1 {
		return nil, validationError("That picture is too large. Use one no bigger than 4096 pixels on a side.")
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, validationError("That picture couldn't be read. Try a different file.")
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func readPhotoBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPhotoUpload))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "That picture is too big. Choose one under 2 MB.")
		return nil, false
	}
	out, err := cleanPhoto(b)
	if err != nil {
		failLDAP(w, err)
		return nil, false
	}
	return out, true
}

// getPhoto serves someone's picture: administrators may see anyone's, everyone else only their own.
func (s *Server) getPhoto(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn := sess.DN
	if q := r.URL.Query().Get("dn"); q != "" {
		d, err := s.userDN(q)
		if err != nil {
			failLDAP(w, err)
			return
		}
		if !sess.IsAdmin && !eqFold(d, sess.DN) {
			fail(w, http.StatusForbidden, "You can only see your own picture.")
			return
		}
		dn = d
	}
	var data string
	if !s.withSvc(w, func(c *ldapclient.Client) error {
		e, err := c.GetAttrs(dn, []string{photoField})
		if err != nil {
			return err
		}
		data = first(e.Values(photoField))
		return nil
	}) {
		return
	}
	if data == "" {
		fail(w, http.StatusNotFound, "No picture.")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(data))
}

func (s *Server) setPhoto(dn string, data []byte, w http.ResponseWriter) bool {
	return s.withSvc(w, func(c *ldapclient.Client) error {
		op := "replace"
		if data == nil {
			op = "delete"
		}
		ch := ldapclient.Change{Op: op, Attr: photoField}
		if data != nil {
			ch.Values = []string{string(data)}
		} else if e, err := c.GetAttrs(dn, []string{photoField}); err == nil && len(e.Values(photoField)) == 0 {
			return nil // nothing to remove
		}
		return c.Modify(dn, []ldapclient.Change{ch})
	})
}

// putOwnPhoto lets people set their own picture when the administrators allow it.
func (s *Server) putOwnPhoto(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if f, ok := s.field(photoField); !ok || !f.UserEditable {
		fail(w, http.StatusForbidden, "You aren't allowed to change your picture.")
		return
	}
	b, ok := readPhotoBody(w, r)
	if !ok || !s.setPhoto(sess.DN, b, w) {
		return
	}
	audit(sess, "photo.set", sess.DN)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) deleteOwnPhoto(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	if f, ok := s.field(photoField); !ok || !f.UserEditable {
		fail(w, http.StatusForbidden, "You aren't allowed to change your picture.")
		return
	}
	if s.setPhoto(sess.DN, nil, w) {
		audit(sess, "photo.remove", sess.DN)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func (s *Server) putUserPhoto(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn, err := s.userDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	b, ok := readPhotoBody(w, r)
	if !ok || !s.setPhoto(dn, b, w) {
		return
	}
	audit(sess, "photo.set", dn)
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) deleteUserPhoto(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	dn, err := s.userDN(r.URL.Query().Get("dn"))
	if err != nil {
		failLDAP(w, err)
		return
	}
	if s.setPhoto(dn, nil, w) {
		audit(sess, "photo.remove", dn)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}
