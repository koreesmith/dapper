VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
.PHONY: dev-ldap backend web build image test e2e
dev-ldap: ; docker compose -f docker-compose.yml -f docker-compose.demo.yml up -d openldap  # the demo directory only (not Dapper)
backend:  ; cd backend && go run ./cmd/dapper
web:      ; cd web && npm run dev
build:    ; cd web && npm run build && cd ../backend && go build -ldflags "-X main.version=$(VERSION)" -o bin/dapper ./cmd/dapper
image:    ; docker build --build-arg VERSION=$(VERSION) -t dapper:$(VERSION) .
test: ; cd backend && go vet ./... && go test ./... && cd ../web && npm run build
e2e:  ; ./scripts/e2e.sh && ./scripts/e2e-unix.sh && ./scripts/e2e-groups.sh && ./scripts/e2e-gid.sh && ./scripts/e2e-security.sh && ./scripts/e2e-bulk.sh && ./scripts/e2e-ou.sh && ./scripts/e2e-password.sh && ./scripts/e2e-overlays.sh
