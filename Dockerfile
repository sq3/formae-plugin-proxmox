FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETARCH
ARG TARGETOS=linux

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o proxmox .

FROM busybox:stable
COPY --from=builder /build/proxmox /plugin/proxmox
COPY schema/pkl/ /plugin/schema/pkl/
COPY formae-plugin.pkl /plugin/formae-plugin.pkl
