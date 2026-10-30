#!/bin/bash
# Build a multi-arch image and push it to the registry.
#
#   ./scripts/build-push.sh              -> :latest
#   ./scripts/build-push.sh v1.2.3       -> :v1.2.3
#
# Secrets are NOT build args - they are runtime env via compose `env_file`, and
# .dockerignore keeps .env out of the image layers.
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

USERNAME="${DOCKER_USERNAME:-ynui12}"
VERSION="${1:-latest}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"

APP_TAG="${USERNAME}/inletd:${VERSION}"

echo "Setting up buildx..."
docker buildx create --use --name multiarch-builder 2>/dev/null || true
docker buildx inspect --bootstrap

echo "Logging in to Docker Hub..."
docker login

echo "Building ${APP_TAG} for ${PLATFORMS}..."
docker buildx build --platform "$PLATFORMS" -f "$PROJECT_DIR/Dockerfile.server" -t "$APP_TAG" "$PROJECT_DIR" --push

echo "Done! Image pushed:"
echo "  - ${APP_TAG}"
