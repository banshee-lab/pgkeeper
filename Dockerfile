# Build the frontend assets inside Docker.
FROM node:22.23.2-bookworm-slim AS web-build
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/yarn.lock ./
RUN yarn install --frozen-lockfile --non-interactive
COPY web/ ./
RUN yarn build

# Embed the generated frontend in the Go executable.
FROM golang:1.27.3-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/build ./web/build
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pgkeeper ./cmd/pgkeeper

# Runtime dependencies for backup and Docker operations.
FROM alpine:3
LABEL org.opencontainers.image.source https://github.com/banshee-lab/pgkeeper

RUN apk add --no-cache ca-certificates docker-cli restic tzdata \
    && install -d /etc/pgkeeper \
    && install -d -m 1777 /var/lib/pgkeeper /var/log

COPY --from=go-build /out/pgkeeper /usr/local/bin/pgkeeper

ENV PGKEEPER_CONFIG=/etc/pgkeeper/pgkeeper.toml
EXPOSE 8080
VOLUME ["/var/lib/pgkeeper"]
# Add the Docker socket's group at runtime (for example with --group-add).
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/pgkeeper"]
