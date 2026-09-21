# glibc base (Debian). The previous python:3.13-alpine (musl) base could not run
# the official ClickHouse binary — it is glibc-linked and crashed with
# "Segmentation fault (core dumped)" on musl (gcompat / alpine-pkg-glibc are
# insufficient for ClickHouse). Every other client we install is glibc-friendly
# on this base too.
FROM public.ecr.aws/docker/library/python:3.13-slim


ENV DEBIAN_FRONTEND=noninteractive

RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && \
    echo 'Acquire::Retries "3";' > /etc/apt/apt.conf.d/80-retries && \
    apt-get update && \
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
    # kafka (kcat — light single-binary metadata/produce/consume client)
    kcat \
    # ssh client
    openssh-client \
    && rm -rf /var/lib/apt/lists/*

# kubectl (no apt package on Debian — install the released binary)
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    K8S_VERSION="v1.32.2" && \
    curl -fsSL --connect-timeout 15 --retry 3 --retry-delay 5 \
    -o /usr/local/bin/kubectl \
    "https://dl.k8s.io/release/${K8S_VERSION}/bin/linux/${ARCH}/kubectl" && \
    chmod +x /usr/local/bin/kubectl

# helm — downloaded from the Red Hat OpenShift mirror. The self-hosted CI
# runner's egress resets connections to get.helm.sh, so neither the get-helm-3
# script nor a direct tarball download works there. mirror.openshift.com provides
# reliable multi-arch tarballs without pulling from Docker Hub.
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --connect-timeout 15 --retry 3 --retry-delay 5 \
    -o helm.tar.gz \
    "https://mirror.openshift.com/pub/openshift-v4/clients/helm/latest/helm-linux-${ARCH}.tar.gz" && \
    tar -xzf helm.tar.gz -C /usr/local/bin "helm-linux-${ARCH}" && \
    mv "/usr/local/bin/helm-linux-${ARCH}" /usr/local/bin/helm && \
    chmod +x /usr/local/bin/helm && \
    rm -f helm.tar.gz

RUN curl -fsSL --connect-timeout 15 --retry 3 --retry-delay 5 \
    -o /usr/bin/rabbitmqadmin \
    "https://raw.githubusercontent.com/rabbitmq/rabbitmq-management/v3.8.9/bin/rabbitmqadmin" && \
    chmod +x /usr/bin/rabbitmqadmin

WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY pdb-node-drain.py .
RUN curl -fsSL --connect-timeout 15 https://sdk.cloud.google.com | bash
ENV PATH=/root/google-cloud-sdk/bin:$PATH
RUN gcloud components install beta alpha --quiet && rm -rf /root/google-cloud-sdk/.install/.backup
RUN pip install --no-cache-dir azure-cli

RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --connect-timeout 15 --retry 3 --retry-delay 5 \
    -o /usr/local/bin/argocd \
    "https://github.com/argoproj/argo-cd/releases/download/v3.0.11/argocd-linux-${ARCH}" && \
    chmod +x /usr/local/bin/argocd

# ClickHouse client — downloaded directly from ClickHouse GitHub releases
# (clickhouse-common-static provides the standalone multicall binary without
# pulling from Docker Hub). Multi-arch: supports amd64 and arm64.
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    CK_VERSION="24.8.10.5" && \
    curl -fsSL --connect-timeout 15 --retry 3 --retry-delay 5 \
    -o /tmp/clickhouse.tgz \
    "https://github.com/ClickHouse/ClickHouse/releases/download/v${CK_VERSION}-lts/clickhouse-common-static-${CK_VERSION}-${ARCH}.tgz" && \
    tar -xzf /tmp/clickhouse.tgz --strip-components=3 -C /usr/local/bin "clickhouse-common-static-${CK_VERSION}/usr/bin/clickhouse" && \
    chmod +x /usr/local/bin/clickhouse && \
    rm -f /tmp/clickhouse.tgz

# mssql-tools (sqlcmd)
RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --connect-timeout 15 --retry 3 --retry-delay 5 \
    -o sqlcmd.tar.bz2 \
    "https://github.com/microsoft/go-sqlcmd/releases/download/v1.10.0/sqlcmd-linux-${ARCH}.tar.bz2" && \
    tar -xjf sqlcmd.tar.bz2 -C /usr/local/bin sqlcmd && \
    chmod +x /usr/local/bin/sqlcmd && \
    rm sqlcmd.tar.bz2

ENTRYPOINT ["bash"]
