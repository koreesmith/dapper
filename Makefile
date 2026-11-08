.PHONY: dev-ldap backend web build test e2e
dev-ldap: ; docker compose -f docker-compose.yml -f docker-compose.demo.yml up -d openldap  # the demo directory only (not Dapper)
backend:  ; cd backend && go run ./cmd/dapper
web:      ; cd web && npm run dev
build:    ; cd web && npm run build && cd ../backend && go build -o bin/dapper ./cmd/dapper
test: ; cd backend && go vet ./... && go test ./... && cd ../web && npm run build
e2e:  ; ./scripts/e2e.sh
