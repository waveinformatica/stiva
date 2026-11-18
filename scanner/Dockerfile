# stiva-scanner: the image analyser of Stiva (docs/scanning.md). A separate
# module and image so Trivy, which it embeds, never weighs on the registry.
# Build context: this directory.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/stiva-scanner .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/stiva-scanner /app/stiva-scanner
USER nonroot:nonroot
ENTRYPOINT ["/app/stiva-scanner"]
