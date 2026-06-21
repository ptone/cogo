# syntax=docker/dockerfile:1
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ARG BASE_IMAGE
FROM golang:1.26-alpine AS builder

# Install git as it is required by Go to resolve dependencies
RUN apk add --no-cache git

ARG COGO_VERSION=latest
RUN go install github.com/go-steer/cogo/cmd/cogo@${COGO_VERSION}

FROM ${BASE_IMAGE}

USER root

# Pre-create standard .agents directories
RUN mkdir -p /home/scion/.agents /home/scion/.agents/skills \
  && chown -R scion:scion /home/scion/.agents

# Copy the compiled cogo binary from the builder stage
COPY --from=builder /go/bin/cogo /usr/local/bin/cogo
RUN chmod +x /usr/local/bin/cogo

USER scion
CMD ["cogo"]
