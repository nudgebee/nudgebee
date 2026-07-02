FROM alpine:3.22

# Install curl for making HTTP requests and bc for floating point math
RUN apk add --no-cache curl bc \
    && adduser -D -g '' appuser

# Copy the monitoring script into the container
COPY monitor.sh /monitor.sh
RUN chmod +x /monitor.sh

# Run as non-root
USER appuser

# The command to run when the container starts
CMD ["/monitor.sh"]
