# Multi-stage build for the KOSMOS registry.

# The web bundle is built here rather than committed as a prebuilt artifact, so
# the UI in the image always corresponds to the source in this tree. Building it
# on the host is fragile: node_modules carries platform-specific native bindings
# and silently belongs to whichever machine installed it.
FROM node:22-alpine AS web
WORKDIR /web
RUN corepack enable && corepack prepare pnpm@11.1.1 --activate
COPY web/package.json web/pnpm-lock.yaml ./
# pnpm refuses to run dependency build scripts unless told to, and exits
# non-zero when it skips any. esbuild needs its own to place the platform
# binary vite compiles with. Allowing them is what npm does by default, and here
# it happens in a throwaway container installing from a frozen lockfile.
RUN pnpm install --frozen-lockfile --config.dangerouslyAllowAllBuilds=true
COPY web/ ./
RUN pnpm build

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/registry ./cmd/registry
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/registry-warmer ./cmd/registry-warmer

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/registry /app/registry
COPY --from=build /out/registry-warmer /app/registry-warmer
COPY --from=web /web/dist /app/web/dist
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/registry"]
