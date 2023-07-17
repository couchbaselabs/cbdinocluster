ARG BASE_IMAGE
FROM $BASE_IMAGE

RUN mkdir -p /etc/couchbase.d && \
    /bin/sh -c 'echo serverless > /etc/couchbase.d/config_profile' && \
    chmod 755 /etc/couchbase.d/config_profile
