FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /bori-operator ./cmd/bori-operator

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bori-operator /bori-operator
ENTRYPOINT ["/bori-operator"]
