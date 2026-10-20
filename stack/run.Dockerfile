# syntax=docker/dockerfile:1
# Run image: glibc, CA certificates, time zones and bash (npm-start and
# Procfile commands run through bash). Everything Node.js needs, including
# its shared libraries, arrives in buildpack layers.
ARG BASE_IMAGE=cgr.dev/chainguard/wolfi-base:latest@sha256:81211e15a86ea1d5357f284482af45730c51de45eaf10ad9ce205d6bfc2df323
FROM ${BASE_IMAGE}

ARG CNB_USER_ID=1000
ARG CNB_GROUP_ID=1000

USER root
RUN apk add --no-cache bash ca-certificates-bundle tzdata \
 && addgroup -g "${CNB_GROUP_ID}" cnb \
 && adduser -D -u "${CNB_USER_ID}" -G cnb -s /bin/bash cnb

ENV CNB_USER_ID=${CNB_USER_ID} \
    CNB_GROUP_ID=${CNB_GROUP_ID} \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

LABEL io.buildpacks.stack.id="io.fckfips.stacks.wolfi" \
      io.buildpacks.base.distro.name="wolfi" \
      io.buildpacks.base.homepage="https://github.com/taha2samy-3/fck-fips-buildpacks-nodejs" \
      io.buildpacks.base.maintainer="taha2samy-3" \
      org.opencontainers.image.source="https://github.com/taha2samy-3/fck-fips-buildpacks-nodejs"

USER ${CNB_USER_ID}:${CNB_GROUP_ID}
