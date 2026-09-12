FROM --platform=linux/amd64 ubuntu:24.04@sha256:a61567bd31828687156d735ea8eb01ba4e37636e225dd6a48ba94136a70d9d61

# BuildKit verifies HTTPS and the pinned Mozilla/curl PEM before APT exists as
# a TLS client. No host trust is copied; ca-certificates later replaces this file.
ADD --checksum=sha256:f66dff1bdf8f96060b8177976f8b7d9254bc89bc4db933d769f7384d28480bc9 --chmod=0644 https://curl.se/ca/cacert-2026-08-13.pem /etc/ssl/certs/ca-certificates.crt

# A dated, signed Ubuntu snapshot pins the complete apt dependency resolution.
# No runtime installation is permitted. Updating this date is a reviewed upgrade.
# Remote ADD also gives its newly created parent directories mode 0644 here.
# Preserve the file's read mode, but allow the unprivileged _apt user to traverse.
RUN chmod 0755 /etc/ssl /etc/ssl/certs && \
    rm -f /etc/apt/sources.list.d/ubuntu.sources && \
    printf '%s\n' 'deb [signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg check-valid-until=no] https://snapshot.ubuntu.com/ubuntu/20260910T000000Z noble main universe' \
      'deb [signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg check-valid-until=no] https://snapshot.ubuntu.com/ubuntu/20260910T000000Z noble-updates main universe' \
      'deb [signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg check-valid-until=no] https://snapshot.ubuntu.com/ubuntu/20260910T000000Z noble-security main universe' > /etc/apt/sources.list && \
    apt-get -o Acquire::https::CaInfo=/etc/ssl/certs/ca-certificates.crt update && \
    apt-get -o Acquire::https::CaInfo=/etc/ssl/certs/ca-certificates.crt install -y --no-install-recommends ca-certificates curl xz-utils git && \
    dpkg-query -W > /opt-apt-versions.txt && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://go.dev/dl/go1.26.8.linux-amd64.tar.gz -o /tmp/go.tgz && \
    echo 'd0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b  /tmp/go.tgz' | sha256sum -c - && \
    tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz && \
    curl -fsSL https://nodejs.org/dist/v24.18.0/node-v24.18.0-linux-x64.tar.xz -o /tmp/node.txz && \
    echo '55aa7153f9d88f28d765fcdad5ae6945b5c0f98a36881703817e4c450fa76742  /tmp/node.txz' | sha256sum -c - && \
    tar -C /usr/local --strip-components=1 -xJf /tmp/node.txz && rm /tmp/node.txz && \
    curl -fsSL https://github.com/anomalyco/opencode/releases/download/v1.18.30/opencode-linux-x64-baseline.tar.gz -o /tmp/opencode.tgz && \
    echo '60c92147d0d86ca606dda8a77260d3c87e0ef959eb2d8dbffb34df6d8a64e063  /tmp/opencode.tgz' | sha256sum -c - && \
    tar -C /usr/local/bin -xzf /tmp/opencode.tgz && rm /tmp/opencode.tgz

ENV PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin GOTOOLCHAIN=local CGO_ENABLED=0 GOMODCACHE=/opt/gomod
WORKDIR /opt/plugin
COPY lab/package.json lab/package-lock.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund && \
    node --input-type=module -e 'import {tool} from "@opencode-ai/plugin"; if (!tool.schema) process.exit(1)'
WORKDIR /opt/source
COPY . .
RUN go mod download && go mod verify && \
    go build -buildvcs=false -trimpath -o /usr/local/bin/lab-bootstrap ./cmd/tools-programmer-lab && \
    go build -buildvcs=false -trimpath -o /tmp/product ./cmd/tools-programmer && \
    rm /tmp/product && chmod -R a+rX /opt/gomod /opt/plugin

# Neither the prep checkout nor root's caches/configuration are runtime inputs.
RUN rm -rf /opt/source /root/.cache /root/.npm && \
    /usr/sbin/useradd --uid 10001 --create-home --shell /bin/bash lab
ENV GOPROXY=off GOSUMDB=off HOME=/tmp/home XDG_CONFIG_HOME=/tmp/config XDG_CACHE_HOME=/tmp/cache XDG_DATA_HOME=/tmp/data XDG_STATE_HOME=/tmp/state GOCACHE=/tmp/gocache
WORKDIR /tmp
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/lab-bootstrap", "bootstrap"]
