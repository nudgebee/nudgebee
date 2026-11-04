# Tooling base for the cloud-collector service: the cloud CLIs it shells out to
# (aws, az, gcloud/gsutil/bq) on Red Hat UBI 9, for the Red Hat container
# certification (#40411). It was Alpine; the CLIs now run on glibc.
#
# Two stages. `tools` is the full ubi9/python-312 image, which has gcc, make, tar and
# unzip; it downloads, verifies and builds everything under /opt and /usr/local. The final
# stage is ubi9/python-312-minimal plus a handful of runtime packages and copies of those
# directories, so no compiler toolchain ships in the runtime image.

# --- Stage 1: build and install the CLIs ---
FROM registry.access.redhat.com/ubi9/python-312 AS tools
USER 0
ARG TARGETARCH

# AWS CLI v2, built from the pinned source tag as a "system-sandbox" install: a venv on the
# system Python 3.12 with the dependency versions AWS locks. The official self-contained
# installer is not used because it bundles its own CPython and setuptools, which AWS patches
# on its own cadence and which the scanners flag (CPython 3.14.8 Critical, setuptools 70.3.0
# High at 2.39.3) with no way for us to upgrade them. Here the interpreter is the UBI one and
# setuptools, pip, wheel and urllib3 are upgraded to patched floors below (the venv scripts
# keep the build-dir shebang, so the old copies are removed first and reinstalled from the
# tools-stage pip; an in-place upgrade left the old dist-info behind, which scanners then read).
# The tarball is a tag archive checked against a SHA-256 taken when the pin was set. To bump: download the new tag archive, update the
# version and hash, and check `aws --version` in the built image.
ARG AWSCLI_VERSION=2.39.3
ARG AWSCLI_SHA256=a1ad8d3c9504b3562bbc08ecd5c10a46960cac8eecaed1f4e4e72fc84ced0b8d
RUN curl -fsSL --retry 5 --retry-all-errors -o /tmp/awscli.tar.gz \
        "https://github.com/aws/aws-cli/archive/refs/tags/${AWSCLI_VERSION}.tar.gz" \
    && echo "${AWSCLI_SHA256}  /tmp/awscli.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/awscli.tar.gz -C /tmp \
    && cd /tmp/aws-cli-${AWSCLI_VERSION} \
    && ./configure --prefix=/opt/aws-cli --with-install-type=system-sandbox --with-download-deps \
    && make \
    && make install \
    && for d in /opt/aws-cli/lib/aws-cli/lib /opt/aws-cli/lib/aws-cli/lib64; do \
         S="${d}/python3.12/site-packages"; \
         rm -rf "${S}"/pip "${S}"/pip-*.dist-info "${S}"/setuptools "${S}"/setuptools-*.dist-info \
                "${S}"/pkg_resources "${S}"/_distutils_hack "${S}"/distutils-precedence.pth \
                "${S}"/wheel "${S}"/wheel-*.dist-info "${S}"/urllib3 "${S}"/urllib3-*.dist-info; \
       done \
    && python3 -m pip --python /opt/aws-cli/lib/aws-cli/bin/python install --no-cache-dir \
        pip "setuptools>=84.2.1" "wheel>=0.46.3" "urllib3>=2.8.0,<3" \
    && cd / && rm -rf /tmp/aws-cli-${AWSCLI_VERSION} /tmp/awscli.tar.gz

# Azure CLI in its own venv on the system interpreter (pip wheels are manylinux, so nothing
# compiles). Install azure-cli and the patched floors in one resolver pass so pip satisfies
# all constraints simultaneously (azure-cli-core declares a bare `cryptography` requirement
# with no ceiling, so >=50 resolves cleanly). This clears the cryptography, PyJWT, urllib3,
# oauthlib and certifi HIGHs that cloud-collector-server inherits from this venv.
# Single-pass is deliberate: if a future azure-cli ever pins below a floor, this fails loudly
# at build instead of silently shipping an unsupported combo.
# Extensions live in a shared directory rather than ~/.azure so the non-root runtime user
# (and an arbitrary OpenShift UID) finds the costmanagement extension without writing it.
ENV AZURE_EXTENSION_DIR=/opt/azure-cli-extensions
RUN /usr/bin/python3.12 -m venv /opt/azure-cli-venv \
    && /opt/azure-cli-venv/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/azure-cli-venv/bin/pip install --no-cache-dir azure-cli "cryptography>=50.0.0" \
        "PyJWT>=2.14.0" "urllib3>=2.8.0" "oauthlib>=4.0.0" "certifi>=2026.10.18" \
    && /opt/azure-cli-venv/bin/az extension add --name costmanagement

# Google Cloud SDK. Run gcloud on a small venv (cryptography>=48) instead of the SDK's bundled
# Python: the bundle ships a frozen cryptography + pip that Google patches on its own cadence,
# so it accrues CVEs we cannot upgrade -- deleting it removes them. gcloud-crc32c is a Go
# binary that only accelerates `gcloud storage` transfers (unused here -- GCS access goes
# through the Go SDK), so it is dead weight carrying stdlib CVEs from Google's build
# toolchain. The tarball is version-pinned and hash-checked (the old image piped the
# unversioned sdk.cloud.google.com installer into bash). To bump: download both tarballs from
# https://cloud.google.com/sdk/docs/install-sdk and update the version and both hashes.
ARG GCLOUD_VERSION=591.0.0
RUN case "${TARGETARCH}" in \
        amd64) GC_ARCH=x86_64; GC_SHA256="78c570f5fcfe58a9311bd98c06b75530595e3cae6277aad75a676a39e6ee7b63" ;; \
        arm64) GC_ARCH=arm;    GC_SHA256="f5e5df0433f77c0e7f5df9fd4bce2e46c7b7967e79cf66e31b1164d3d4c86546" ;; \
        *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL --retry 5 --retry-all-errors -o /tmp/gcloud.tar.gz \
        "https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-${GCLOUD_VERSION}-linux-${GC_ARCH}.tar.gz" \
    && echo "${GC_SHA256}  /tmp/gcloud.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/gcloud.tar.gz -C /usr/local \
    && rm /tmp/gcloud.tar.gz \
    && rm -rf /usr/local/google-cloud-sdk/platform/bundledpythonunix /usr/local/google-cloud-sdk/bin/gcloud-crc32c \
    && /usr/bin/python3.12 -m venv /opt/gcloud-venv \
    && /opt/gcloud-venv/bin/pip install --no-cache-dir --upgrade pip "cryptography>=48.0.1" "certifi>=2026.10.18" "urllib3>=2.8.0"

# tini as PID 1: CLI tools invoked by this server (az, gcloud, ...) can spawn background
# subprocesses they never wait on. Once their parent exits, those subprocesses are
# reparented to PID 1. Without a real init there, they become permanent zombies until the
# container hits its process-count ceiling (#36530). tini is not in the UBI repos, so use
# the pinned, checksum-verified static binary (same as llm/llm-server).
ARG TINI_VERSION=v0.19.0
RUN case "${TARGETARCH}" in \
        amd64) TINI_SHA256="c5b0666b4cb676901f90dfcb37106783c5fe2077b04590973b885950611b30ee" ;; \
        arm64) TINI_SHA256="eae1d3aa50c48fb23b8cbdf4e369d0910dfc538566bfd09df89a774aa84a48b9" ;; \
        *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL --retry 5 --retry-all-errors -o /usr/local/bin/tini \
        "https://github.com/krallin/tini/releases/download/${TINI_VERSION}/tini-static-${TARGETARCH}" \
    && echo "${TINI_SHA256}  /usr/local/bin/tini" | sha256sum -c - \
    && chmod 0755 /usr/local/bin/tini

# --- Stage 2: runtime ---
FROM registry.access.redhat.com/ubi9/python-312-minimal
USER 0

# OS-package cache-bust: bumping OS_PKG_EPOCH (ISO week) changes this layer's cache key so
# BuildKit re-runs the upgrade and pulls current security patches -- its registry cache
# (mode=max) otherwise serves a stale package layer forever. Committed (not a build-arg) so
# each refresh is a reviewable, revertable diff; bump it together with the epoch in the other
# images. microdnf has no --security flag, so this is a full upgrade.
# jq is the only extra package; bash and curl ship in the base image. The base image's own
# pip is upgraded too, as it carries open pip advisories.
ARG OS_PKG_EPOCH=2026-W44
RUN echo "os-pkg-epoch: ${OS_PKG_EPOCH}" \
    && microdnf -y upgrade --refresh \
    && microdnf -y install --setopt=install_weak_deps=0 --nodocs jq \
    && microdnf clean all && rm -rf /var/cache/yum /var/cache/dnf \
    && python3 -m pip install --no-cache-dir --upgrade pip

COPY --from=tools /opt/aws-cli /opt/aws-cli
COPY --from=tools /opt/azure-cli-venv /opt/azure-cli-venv
COPY --from=tools /opt/azure-cli-extensions /opt/azure-cli-extensions
COPY --from=tools /opt/gcloud-venv /opt/gcloud-venv
COPY --from=tools /usr/local/google-cloud-sdk /usr/local/google-cloud-sdk
COPY --from=tools /usr/local/bin/tini /usr/local/bin/tini

# Put the CLIs on PATH. The aws and az venvs both use /usr/bin/python3.12, identical in this
# image to the one they were built with.
RUN ln -s /opt/aws-cli/bin/aws /usr/local/bin/aws \
    && ln -s /opt/aws-cli/bin/aws_completer /usr/local/bin/aws_completer \
    && ln -s /opt/azure-cli-venv/bin/az /usr/local/bin/az

ENV AZURE_EXTENSION_DIR=/opt/azure-cli-extensions
ENV CLOUDSDK_PYTHON=/opt/gcloud-venv/bin/python
ENV PATH="/usr/local/google-cloud-sdk/bin:$PATH"
# urllib3 2.8.0 warns on the `strict` argument botocore still passes, once per aws call.
# Silence that one warning so it stays out of CLI results.
ENV PYTHONWARNINGS=ignore::FutureWarning:urllib3.poolmanager

# OpenShift runs the container as a random UID in group 0. The tools keep their state under
# $HOME (~/.aws, ~/.azure, ~/.config/gcloud), which the base image already makes group-0
# writable; the shared tool directories stay root-owned and read-only.
USER 1001
