FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETARCH
ARG TARGETOS=linux

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o proxmox .

FROM busybox:stable
COPY --from=builder /build/proxmox /plugin/proxmox/v0.1.0/proxmox
COPY schema/pkl/ /plugin/proxmox/v0.1.0/schema/pkl/
COPY formae-plugin.pkl /plugin/proxmox/v0.1.0/formae-plugin.pkl
