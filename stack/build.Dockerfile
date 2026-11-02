# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
# Build image: where buildpacks run. It carries the toolchain native addons
# need (node-gyp: python, make, a C/C++ compiler). Node.js, npm and pnpm are
# not here; the buildpacks install them into layers.
ARG BASE_IMAGE=cgr.dev/chainguard/wolfi-base:latest@sha256:81211e15a86ea1d5357f284482af45730c51de45eaf10ad9ce205d6bfc2df323
FROM ${BASE_IMAGE}

ARG CNB_USER_ID=1000
ARG CNB_GROUP_ID=1000

USER root
RUN apk add --no-cache \
      bash \
      build-base \
      ca-certificates-bundle \
      git \
      python-3 \
      tzdata \
 && addgroup -g "${CNB_GROUP_ID}" cnb \
 && adduser -D -u "${CNB_USER_ID}" -G cnb -s /bin/bash cnb

# The installed package list goes into the image: the stack workflow compares
# it with the published image to decide whether a rebuild changed anything.
RUN mkdir -p /usr/share/fckfips && apk info -v | sort > /usr/share/fckfips/stack-packages.txt

ENV CNB_USER_ID=${CNB_USER_ID} \
    CNB_GROUP_ID=${CNB_GROUP_ID} \
    CNB_STACK_ID=io.fckfips.stacks.wolfi

LABEL io.buildpacks.stack.id="io.fckfips.stacks.wolfi" \
      io.buildpacks.base.distro.name="wolfi" \
      io.buildpacks.base.homepage="https://github.com/taha2samy-3/fck-fips-buildpack-nodejs" \
      io.buildpacks.base.maintainer="taha2samy-3" \
      org.opencontainers.image.source="https://github.com/taha2samy-3/fck-fips-buildpack-nodejs"

USER ${CNB_USER_ID}:${CNB_GROUP_ID}
