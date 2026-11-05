# The binary is built on the host by deploy.sh, so the Pi needs no Go image.
FROM scratch
COPY nunchaku /nunchaku
USER 1000:1000
EXPOSE 3169
HEALTHCHECK --interval=30s --timeout=5s --start-period=3s --retries=3 CMD ["/nunchaku", "-healthcheck"]
ENTRYPOINT ["/nunchaku"]
