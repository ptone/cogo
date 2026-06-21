# Scion Integration for Cogo

This directory contains the [Scion](https://github.com/GoogleCloudPlatform/scion) harness configuration for **Cogo**, allowing Cogo to be run inside containerized agent sandboxes driven by the Scion tool.

## Installation

To install this harness locally in your Scion instance, run the following command from your machine:

```sh
scion harness-config install file:///Users/ptone/src/cogo/scion
```

To install from GitHub (once this branch is merged to `main` upstream):

```sh
scion harness-config install https://github.com/go-steer/cogo/tree/main/scion
```

## Auth Modes

The harness supports the following authentication configurations:

| Mode | Required Environment Variables | Description |
|------|--------------------------------|-------------|
| `api-key` (default) | `GOOGLE_API_KEY` | Public Gemini API key |
| `vertex-ai` | `GOOGLE_CLOUD_PROJECT` & `GOOGLE_CLOUD_LOCATION` | Vertex AI with Application Default Credentials (ADC) |

## Directory Contents

- **`config.yaml`**: Standard declarative harness configuration defining capabilities, auth (public API and Vertex AI), instructions mapping, and MCP support.
- **`provision.py`**: A dependency-free container-side Python script that handles credential loading, folder creation, `AGENTS.md` and system prompt construction, and `.agents/mcp.json` mapping.
- **`Dockerfile`**: Image definition compiling on top of `scion-base` while copying the compiled binary and ensuring proper folder ownership.
- **`cloudbuild.yaml`**: Buildx multi-arch pipeline configuration.

## Manual Build & Run

Since the Dockerfile is configured as a multi-stage build, it compiles the binary directly from GitHub during container construction.

To build the local Docker image:

```sh
# Build the local Docker image (by default compiles @latest)
docker build --build-arg BASE_IMAGE=scion-base:latest -t scion-cogo:latest scion/

# To compile a specific branch, tag, or commit (e.g. 'scion-harness'):
docker build \
  --build-arg BASE_IMAGE=scion-base:latest \
  --build-arg COGO_VERSION=scion-harness \
  -t scion-cogo:latest scion/
```
