 FROM python:3.13-alpine

RUN apk update && \
    apk add --no-cache \
    # build/code
    build-base git bash bash-completion ncurses vim tmux jq less gawk linux-headers\
    # network
    bind-tools iputils tcpdump curl nmap tcpflow iftop net-tools mtr netcat-openbsd bridge-utils iperf ngrep sed \
    # certificates
    ca-certificates openssl \
    # processes/io
    htop atop strace iotop sysstat ltrace ncdu logrotate hdparm pciutils psmisc tree pv util-linux \
    # kubernetes
    kubectl \
    # helm
    helm \
    # pg
    postgresql-client \
    # aws-cli
    aws-cli \
    # redis
    redis \
    # control process
    openrc \
    # mysql client
    mysql-client \
    mariadb-connector-c \
    openssh \
    # glibc compat for glibc-compiled binaries (e.g. sqlcmd)
    gcompat


RUN curl -fsSL --retry 3 --retry-delay 5 \
    -o /usr/bin/rabbitmqadmin \
    "https://raw.githubusercontent.com/rabbitmq/rabbitmq-management/v3.8.9/bin/rabbitmqadmin" && \
    chmod +x /usr/bin/rabbitmqadmin

WORKDIR /app  
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY pdb-node-drain.py .
#RUN pip install openssl-dev libffi-dev gcc musl-dev make
RUN curl https://sdk.cloud.google.com | bash
ENV PATH=/root/google-cloud-sdk/bin:$PATH
RUN pip install --no-cache-dir azure-cli

RUN ARCH=$([ "$(uname -m)" = "x86_64" ] && echo "amd64" || echo "arm64") && \
    curl -fsSL --retry 3 --retry-delay 5 \
    -o /usr/local/bin/argocd \
    "https://github.com/argoproj/argo-cd/releases/download/v3.0.11/argocd-linux-${ARCH}" && \
    chmod +x /usr/local/bin/argocd

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
