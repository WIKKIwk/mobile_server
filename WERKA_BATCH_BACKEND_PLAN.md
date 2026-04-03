# Werka Batch Backend Plan

## Goal

Add a minimal backend batch flow for Werka so the mobile app can submit
multiple customer shipment lines in one request while keeping the current
Delivery Note business model unchanged.

## Core Rule

- one batch line equals one Delivery Note

We are not introducing a new shipment document type.

## Current Single-Line Path

Existing path:

- `POST /v1/mobile/werka/customer-issue/create`
- `handleWerkaCustomerIssueCreate`
- `CreateWerkaCustomerIssue`

That existing method already does the real business work:

- validate role
- resolve customer
- verify item assignment for that customer
- resolve item details
- resolve warehouse and company
- create draft Delivery Note
- write `accord_*` state
- submit the Delivery Note
- clean up draft on failure

The batch version should reuse this path instead of replacing it.

## Architecture Decision

The first implementation must be a thin wrapper around the current
single-create method.

That means:

- minimal new code
- no new ERP creation algorithm
- no new storage layer
- no new batch document
- no parallel worker pool in the first version

## Why Serial Execution First

We want correctness before throughput.

Serial line execution gives us:

- deterministic behavior
- lower ERP write pressure
- simpler negative-stock handling
- clear per-line error reporting
- less risk of race conditions

Even when the user submits 10 lines at once, that is still a valid batch flow.
The request is one batch command, but the server executes the Delivery Note
creation one line at a time.

## Minimal Backend Scope

### Types

Add:

- `WerkaCustomerIssueBatchCreateRequest`
- `WerkaCustomerIssueBatchLine`
- `WerkaCustomerIssueBatchResult`
- `WerkaCustomerIssueBatchLineResult`

### HTTP

Add endpoint:

- `POST /v1/mobile/werka/customer-issue/batch-create`

### Service

Add:

- `CreateWerkaCustomerIssueBatch`

That method should:

- require Werka role
- validate request shape
- reject empty line lists
- reject non-positive qty
- run lines in order
- call existing `CreateWerkaCustomerIssue(...)` for each line
- collect `created[]`
- collect `failed[]`

## First Acceptance Target

First slice is good enough when:

- batch request reaches the backend
- backend can create multiple Delivery Notes through the existing single flow
- one failed line does not erase already-created lines
- response clearly shows which lines succeeded and failed
- targeted tests pass

## Explicit Non-Goals For First Slice

- no idempotency key yet
- no parallel line execution
- no progress streaming
- no shared transaction across all lines
- no extra persistence for batch history

## Next Slice After This

If the first slice is stable, then we can consider:

- request idempotency via `client_batch_id`
- reusing resolved customer/item lookups across lines
- better error codes
- runtime notification grouping
