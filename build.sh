#!/usr/bin/env bash

for os in linux darwin; do
  for arch in amd64 arm64; do
    GOOS=$os GOARCH=$arch go build -o gls_${os}_${arch} cmd/main.go
  done
done
