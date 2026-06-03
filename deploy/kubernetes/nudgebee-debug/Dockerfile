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

# helm (direct release tarball — the get-helm-3 script hits the GitHub API for
# version detection and is flaky/rate-limited on the self-hosted CI runner)
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    HELM_VERSION=v3.21.0 && \
    curl -fsSL --retry 3 --retry-delay 5 \
    "https://get.helm.sh/helm-${HELM_VERSION}-linux-${ARCH}.tar.gz" -o helm.tar.gz && \
    tar -xzf helm.tar.gz -C /usr/local/bin --strip-components=1 "linux-${ARCH}/helm" && \
    chmod +x /usr/local/bin/helm && \
    rm -f helm.tar.gz

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
RUN pip install --no-cache-dir azure-cli

RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --retry 3 --retry-delay 5 \
    -o /usr/local/bin/argocd \
    "https://github.com/argoproj/argo-cd/releases/download/v3.0.11/argocd-linux-${ARCH}" && \
    chmod +x /usr/local/bin/argocd

# ClickHouse client (glibc binary — now runs because the base is glibc)
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "aarch64") && \
    curl -fsSL "https://builds.clickhouse.com/master/$ARCH/clickhouse" -o /usr/local/bin/clickhouse && \
    chmod +x /usr/local/bin/clickhouse

# mssql-tools (sqlcmd)
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --retry 3 --retry-delay 5 \
    -o sqlcmd.tar.bz2 \
    "https://github.com/microsoft/go-sqlcmd/releases/download/v1.10.0/sqlcmd-linux-${ARCH}.tar.bz2" && \
    tar -xjf sqlcmd.tar.bz2 -C /usr/local/bin sqlcmd && \
    chmod +x /usr/local/bin/sqlcmd && \
    rm sqlcmd.tar.bz2

ENTRYPOINT ["bash"]
