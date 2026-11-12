# syntax=docker/dockerfile:1

# 빌드 단계: 공식 Go 이미지에서 정적 바이너리를 만든다.
FROM golang:1.27.4-alpine3.24 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# 실행 단계: 셸이 없는 최소 이미지에서 root가 아닌 사용자로 실행한다.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/server /server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
