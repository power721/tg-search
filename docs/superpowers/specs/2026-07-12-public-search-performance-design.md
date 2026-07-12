# Public Search Performance Design

## Problem

Public searches with images enabled show highly variable latency. A representative request returned 30 mobile resources in 13.07 seconds:

    keyword="" cloud_types=["mobile"] include_image=true
    include_media_metadata=true limit=30 total=616 returned=30

The indexed resource query is not the bottleneck. Production query plans use
idx_resource_index_type_datetime for both counting and listing mobile
resources. The latency is introduced after the indexed page is loaded.

For every returned link, the current external-search media enrichment performs
an individual file lookup. Each image URL then calls
APIKeyService.EnsureActive, which reads the active API key and the encryption
secret before signing the URL. In the observed page, 27 of 30 results had media,
so one request can issue roughly 87 serialized database operations:

- 3 indexed resource operations: count, grouped count, and page list;
- 30 per-result file lookups;
- about 54 API-key and settings reads for 27 signed image URLs.

The database is configured with one open connection. Background Telegram sync
and maintenance writes therefore amplify these small reads into seconds of
queueing.

## Goals

- Keep the public search response contract unchanged.
- Preserve signed image and video URL behavior.
- Reduce media enrichment to one batch file lookup per result page.
- Load the media-signing key at most once per public search response.
- Preserve include_image=false behavior without unnecessary link-image lookups.
- Keep the change scoped to public search.

## Non-Goals

- Changing resource-index ranking, pagination, totals, or SQL filters.
- Adding a process-wide API-key cache or changing key regeneration behavior.
- Refactoring media enrichment for admin, remote-search, or other API routes.
- Changing the database connection-pool configuration.

## Considered Approaches

### 1. Request-scoped batching and signing

Batch-fetch files for all returned message references, load the signing key
once, and use it to sign every media URL in the response.

This is the selected approach. It addresses both sources of repeated database
access while keeping cache lifetime and invalidation concerns out of scope.

### 2. Batch only the file lookup

Reuse the existing batch file repository method but keep calling EnsureActive
for every generated URL. This removes the per-result file-query N+1 pattern but
leaves two database reads per signed URL, so it does not address the full
production symptom.

### 3. Cache the active signing key globally

Add a service-wide decrypted-key cache and invalidate it when API keys are
regenerated. This could benefit every media-producing route, but introduces
cross-request secret lifetime, concurrency, and invalidation behavior that is
not required for the reported public-search problem.

## Design

### Request-scoped media enrichment

attachMediaToExternalResourceItems will become a page-oriented operation:

1. Identify items that need media processing.
   - File resources keep using their inline file metadata so video links remain
     available even when images are not requested.
   - Link resources participate only when include_image=true.
2. Deduplicate the remaining channel_id and telegram_message_id references.
3. Fetch all associated files with FileRepository.FindByMessageRefs in one query.
4. Determine image and video Telegram file IDs from inline or fetched files.
5. Lazily load the active API key once if at least one signed URL is required.
6. Generate all signed URLs with the same request-scoped signer.
7. Attach only the URLs allowed by the external-search options.

The resource-index query, result ordering, pagination, metadata conversion, and
JSON response construction remain unchanged.

### Request-scoped signer

Media URL signing will be separated into two operations:

- create a signer by loading the active plaintext API key once;
- sign any number of paths using that signer.

Unsigned URL behavior remains direct path construction and does not load an API
key. The signer remains local to one request and is never stored globally. All
generated URLs retain the existing expiration and HMAC format, so
VerifyMediaSignature remains compatible.

### Error handling

- A batch file-query failure fails the request exactly as the existing
  per-result query failure does.
- Failure to load or decrypt the active API key fails the request before
  returning partially signed results.
- URL-signing errors propagate through the existing external-search error path.
- Empty result pages and pages without eligible media avoid both the batch query
  and signing-key lookup.

## Testing

Tests will cover:

- multiple public-search link results receive valid signed image URLs;
- file resources still receive signed video URLs when images are disabled;
- image-disabled link-only pages do not require media enrichment;
- a request-scoped signer produces URLs accepted by the existing signature
  verifier;
- existing external-search response, filtering, metadata, and access-log tests
  continue to pass.

A focused benchmark will compare public-search media enrichment for a
representative 30-item page. The acceptance criterion is structural rather than
wall-clock based: one batch file query and one signing-key load replace
per-result database access, avoiding timing-sensitive assertions in tests.

## Expected Impact

For the observed 30-item mobile page, database operations should fall from
roughly 87 to roughly 6: the three existing resource-index operations, one batch
file query, and the two reads in one signing-key load sequence. This removes the latency
multiplier caused by the single database connection while preserving API
behavior.
