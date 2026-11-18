# Phase 4 PoC Lambda authorizer image.
#
# Build:  make image-authorizer   (repo-root context — needs both modules)
# Push:   make image-push-authorizer; the custom-authorizer-gateway
#         terraform module (rosa-seaplane) references the image URI.

FROM golang:1.26 AS build
WORKDIR /src
# The authorizer module replaces the sibling seaplane-api module with a
# local path (../seaplane-api), so both modules must sit side by side.
COPY seaplane-api/go.mod seaplane-api/go.sum ./seaplane-api/
COPY seaplane-authorizer/go.mod seaplane-authorizer/go.sum ./seaplane-authorizer/
RUN cd seaplane-authorizer && go mod download
COPY seaplane-authorizer/ ./seaplane-authorizer/
COPY seaplane-api/pkg/linking/ ./seaplane-api/pkg/linking/
RUN cd seaplane-authorizer && CGO_ENABLED=0 GOOS=linux go build -o /out/bootstrap ./cmd/authorizer

FROM public.ecr.aws/lambda/provided:al2023
COPY --from=build /out/bootstrap ${LAMBDA_TASK_ROOT}/bootstrap
