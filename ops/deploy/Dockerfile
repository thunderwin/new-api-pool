# 极简镜像：二进制在本地交叉编译好再传上来，服务器不做任何编译。
# 这样 1 vCPU 的小机器也能秒级构建。
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata wget \
    && rm -rf /var/lib/apt/lists/* \
    && update-ca-certificates

COPY new-api /new-api

EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/new-api"]
CMD ["--log-dir", "/app/logs"]
