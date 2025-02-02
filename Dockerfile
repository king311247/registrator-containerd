FROM docker.m.daocloud.io/library/golang:1.21-alpine AS builder
WORKDIR /go/src/github.com/king311247/registrator/
COPY . .

RUN export CGO_ENABLED=0 \
    && export GO111MODULE=on \
    && export GOPROXY=https://goproxy.cn,direct \
    && go build -a -installsuffix cgo -ldflags "-X main.Version=$(cat VERSION)" -o bin/registrator

FROM docker.m.daocloud.io/library/alpine:3.13
RUN apk add --no-cache ca-certificates \
        && echo "hosts: files dns" > /etc/nsswitch.conf
COPY --from=builder /go/src/github.com/king311247/registrator/bin/registrator /bin/registrator

ENTRYPOINT ["/bin/registrator"]