.PHONY: dev-ldap backend web build
dev-ldap: ; docker compose up -d openldap
backend:  ; cd backend && go run ./cmd/dapper
web:      ; cd web && npm run dev
build:    ; cd web && npm run build && cd ../backend && go build -o bin/dapper ./cmd/dapper
