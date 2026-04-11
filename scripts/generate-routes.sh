#!/usr/bin/env bash
# generate-routes.sh — Helper to generate a route configuration entry.
#
# Usage:
#   ./scripts/generate-routes.sh \
#     --id user-profile \
#     --path /api/v1/users/{userID} \
#     --methods GET,PUT \
#     --service user-service \
#     --url http://user-service:8082 \
#     --auth \
#     --timeout 30s \
#     --rps 100 \
#     --burst 200

set -euo pipefail

ID=""
PATH_PATTERN=""
METHODS="GET"
SERVICE_NAME=""
SERVICE_URL=""
STRIP_PREFIX=""
TIMEOUT="30s"
AUTH_REQUIRED="false"
RPS=""
BURST=""

usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --id         <id>        Route ID (required)"
    echo "  --path       <pattern>   URL path pattern (required)"
    echo "  --methods    <methods>   Comma-separated HTTP methods (default: GET)"
    echo "  --service    <name>      Service name (required)"
    echo "  --url        <url>       Service URL"
    echo "  --strip      <prefix>    Strip prefix from path"
    echo "  --timeout    <duration>  Request timeout (default: 30s)"
    echo "  --auth                   Require authentication"
    echo "  --rps        <float>     Rate limit (requests/sec)"
    echo "  --burst      <int>       Rate limit burst size"
    echo "  --help                   Show this help"
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --id)       ID="$2";           shift 2 ;;
        --path)     PATH_PATTERN="$2"; shift 2 ;;
        --methods)  METHODS="$2";      shift 2 ;;
        --service)  SERVICE_NAME="$2"; shift 2 ;;
        --url)      SERVICE_URL="$2";  shift 2 ;;
        --strip)    STRIP_PREFIX="$2"; shift 2 ;;
        --timeout)  TIMEOUT="$2";      shift 2 ;;
        --auth)     AUTH_REQUIRED="true"; shift ;;
        --rps)      RPS="$2";          shift 2 ;;
        --burst)    BURST="$2";        shift 2 ;;
        --help)     usage ;;
        *)          echo "Unknown option: $1"; usage ;;
    esac
done

if [[ -z "$ID" || -z "$PATH_PATTERN" || -z "$SERVICE_NAME" ]]; then
    echo "Error: --id, --path, and --service are required"
    usage
fi

# Convert comma-separated methods to YAML list.
IFS=',' read -ra METHOD_ARRAY <<< "$METHODS"
METHODS_YAML=""
for method in "${METHOD_ARRAY[@]}"; do
    METHODS_YAML="${METHODS_YAML}    - ${method}"$'\n'
done

# Build the YAML output.
echo "  - id: ${ID}"
echo "    path: ${PATH_PATTERN}"
echo "    methods:"
printf "%s" "$METHODS_YAML"
echo "    service_name: ${SERVICE_NAME}"

if [[ -n "$SERVICE_URL" ]]; then
    echo "    service_url: \"${SERVICE_URL}\""
fi

if [[ -n "$STRIP_PREFIX" ]]; then
    echo "    strip_prefix: ${STRIP_PREFIX}"
fi

echo "    timeout: ${TIMEOUT}"
echo "    auth_required: ${AUTH_REQUIRED}"

if [[ -n "$RPS" ]]; then
    echo "    rate_limit:"
    echo "      rps: ${RPS}"
    if [[ -n "$BURST" ]]; then
        echo "      burst: ${BURST}"
    fi
fi
