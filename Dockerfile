# The image is the static binary and nothing else. The binary is built outside it, for the machine the
# image is for: by build-image.sh for a release (arm64 and amd64), by deploy.sh --build on the Pi.
FROM scratch
COPY nunchaku /nunchaku
USER 1000:1000
EXPOSE 3169
HEALTHCHECK --interval=30s --timeout=5s --start-period=3s --retries=3 CMD ["/nunchaku", "-healthcheck"]
ENTRYPOINT ["/nunchaku"]
