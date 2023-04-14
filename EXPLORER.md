# Build and run

sass internal/api/ui/login/static/resources/themes/scss/explorer.scss internal/api/ui/login/static/resources/themes/zitadel/css/explorer.css

goreleaser build --id prod --snapshot --single-target --rm-dist --output .artifacts/zitadel/zitadel

DOCKER_BUILDKIT=1 docker build --file build/Dockerfile .artifacts/zitadel -t zitadel:local

ZITADEL_IMAGE=zitadel:local docker compose --file e2e/docker-compose.yaml up  zitadel


# Debug local

ssh -L 8080:127.0.0.1:8080 username@address

# TODO

- texts in emails (admin)
- texts, TOS, privacy policy links in forms (admin)
- replace ZITADEL in texts (admin)
