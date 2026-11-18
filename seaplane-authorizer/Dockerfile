# Phase 4 PoC Lambda authorizer image.
#
# Build:  docker build -t seaplane-authorizer .
# Push:   tag and push to the regional ECR; the custom-authorizer-gateway
#         terraform module (rosa-seaplane) references the image URI.

FROM golang:1.26 AS build
WORKDIR /src
COPY seaplane-authorizer/go.mod seaplane-authorizer/go.sum ./
# The module replaces the sibling seaplane-api module with a local path.
COPY seaplane-api/go.mod seaplane-api/go.sum ./seaplane-api/
RUN go mod download
COPY seaplane-authorizer/ ./
COPY seaplane-api/pkg/linking/ ./seaplane-api/pkg/linking/
RUN CGO_ENABLED=GOOS=linux go build -o /out/bootstrap ./cmd/authorizer

FROM public.ecr.aws/lambda/provided:al2023
COPY --from=build /out/bootstrap ${LAMBDA_TASK_ROOT}/bootstrap
