package session

import (
	"testing"
	"time"

	"github.com/koreesmith/dapper/backend/internal/ldapclient"
)

func TestRevokeFor(t *testing.T) {
	st := NewStore(time.Hour, time.Hour)
	mk := func(dn string) *Session {
		return st.Create(dn, false, &ldapclient.Client{}) // no connection: sessions only need something to close
	}
	a1, a2, b := mk("uid=a,ou=p"), mk("UID=A,OU=P"), mk("uid=b,ou=p")
	if n := st.RevokeFor("uid=a,ou=p", a1.ID); n != 1 {
		t.Fatalf("expected to end 1 other session, ended %d", n)
	}
	if st.Get(a1.ID) == nil || st.Get(a2.ID) != nil || st.Get(b.ID) == nil {
		t.Error("only the other session of the same account (any DN case) should end")
	}
	if n := st.RevokeFor("uid=a,ou=p", ""); n != 1 || st.Get(a1.ID) != nil || st.Get(b.ID) == nil {
		t.Errorf("ending all of one account's sessions: %d", n)
	}
}
