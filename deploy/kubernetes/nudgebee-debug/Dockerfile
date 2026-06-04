# glibc base (Debian). The previous python:3.13-alpine (musl) base could not run
# the official ClickHouse binary — it is glibc-linked and crashed with
# "Segmentation fault (core dumped)" on musl (gcompat / alpine-pkg-glibc are
# insufficient for ClickHouse). Every other client we install is glibc-friendly
# on this base too.
FROM python:3.13-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    # build/code
    build-essential git bash bash-completion ncurses-bin vim tmux jq less gawk \
    # network
    dnsutils iputils-ping tcpdump curl nmap tcpflow iftop net-tools iproute2 mtr-tiny netcat-openbsd bridge-utils iperf ngrep sed \
    # certificates
    ca-certificates openssl \
    # processes/io
    htop atop strace iotop sysstat ltrace ncdu logrotate hdparm pciutils psmisc tree pv util-linux procps \
    # pg
    postgresql-client \
    # aws-cli
    awscli \
    # redis (redis-cli)
    redis-tools \
    # mysql / mariadb client
    default-mysql-client \
    # ssh client
    openssh-client \
    && rm -rf /var/lib/apt/lists/*

# kubectl (no apt package on Debian — install the released binary)
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --retry 3 --retry-delay 5 \
    -o /usr/local/bin/kubectl \
    "https://dl.k8s.io/release/$(curl -fsSL https://dl.k8s.io/release/stable.txt)/bin/linux/${ARCH}/kubectl" && \
    chmod +x /usr/local/bin/kubectl

# helm — copied from the upstream alpine/helm image on Docker Hub (already
# reachable: the base image is pulled from there). The self-hosted CI runner's
# egress resets connections to get.helm.sh, so neither the get-helm-3 script nor
# a direct tarball download works there. helm is a static Go binary, so the
# musl-built binary runs fine on this glibc base. Multi-arch: buildx selects the
# matching-arch alpine/helm automatically per target platform.
COPY --from=alpine/helm:3.21.0 /usr/bin/helm /usr/local/bin/helm

RUN curl -fsSL --retry 3 --retry-delay 5 \
    -o /usr/bin/rabbitmqadmin \
    "https://raw.githubusercontent.com/rabbitmq/rabbitmq-management/v3.8.9/bin/rabbitmqadmin" && \
    chmod +x /usr/bin/rabbitmqadmin

WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY pdb-node-drain.py .
RUN curl https://sdk.cloud.google.com | bash
ENV PATH=/root/google-cloud-sdk/bin:$PATH
RUN gcloud components install beta alpha --quiet && rm -rf /root/google-cloud-sdk/.install/.backup
RUN pip install --no-cache-dir azure-cli

RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --retry 3 --retry-delay 5 \
    -o /usr/local/bin/argocd \
    "https://github.com/argoproj/argo-cd/releases/download/v3.0.11/argocd-linux-${ARCH}" && \
    chmod +x /usr/local/bin/argocd

# ClickHouse client — copied from the official clickhouse image on Docker Hub
# (already reachable; same approach as helm). This pins the version (vs the
# rolling /master build, which is non-reproducible and can ship a broken binary)
# and drops the builds.clickhouse.com host. clickhouse is a glibc binary, so it
# runs on this glibc base. Multi-arch: buildx selects the matching arch.
COPY --from=clickhouse/clickhouse-server:24.8 /usr/bin/clickhouse /usr/local/bin/clickhouse

# mssql-tools (sqlcmd)
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --retry 3 --retry-delay 5 \
    -o sqlcmd.tar.bz2 \
    "https://github.com/microsoft/go-sqlcmd/releases/download/v1.10.0/sqlcmd-linux-${ARCH}.tar.bz2" && \
    tar -xjf sqlcmd.tar.bz2 -C /usr/local/bin sqlcmd && \
    chmod +x /usr/local/bin/sqlcmd && \
    rm sqlcmd.tar.bz2

ENTRYPOINT ["bash"]
