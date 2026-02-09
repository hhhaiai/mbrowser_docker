# Changelog

All notable changes to this project will be documented in this file.

## [0.1.0] - 2026-02-09

### Added
- Initial MIUI proxy service implementation with OpenAI-compatible `POST /v1/chat/completions` and `POST /v1/responses` endpoints.
- Claude-compatible `POST /v1/messages` endpoint.
- Health endpoint `GET /health`.
- SSE streaming support for OpenAI and Claude style responses.
- Conversation persistence with SQLite (WAL enabled), in-memory conversation cache, and periodic cleanup/eviction.
- Per-user identity mapping based on `Authorization` and session mapping based on `ConversationId`.
- Request options support for deep-thinking/search flags via request body, headers, and model suffixes.
- Dockerfile and local build script for deployment and packaging.

### Notes
- Requested model names are normalized to upstream `DOUBAO`.
