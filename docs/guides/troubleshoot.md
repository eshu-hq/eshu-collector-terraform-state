# Troubleshoot

Start with the symptom you have. Most of these end at a config
value, so keep [Configuration](../reference/configuration.md) open
in another tab.

- Partial result with a `state-missing` warning? A state object
  named by discovery wasn't there. Check the bucket filters.
- Claim keeps coming back retryable? S3 is throttling. Past a
  retry queue bound of 8 the collector fails terminal instead of
  queuing forever, so repeated retries point at the bucket, not
  the collector.
- Claim fails closed before reading anything? The candidate
  bucket sits outside the target-scope allow-list. Fix
  `target_scopes` rather than widening discovery blindly.
- Endpoint override refused? Overrides must be credential-free
  base URLs with no query or fragment; instance-metadata
  addresses are refused outright.

## Still stuck?

Re-read the failure flow in
[Diagrams](../diagrams/README.md#3-when-something-goes-wrong) and
check which branch your run took. If the result looks complete but
wrong, compare it against [Facts](../reference/facts.md) before
suspecting the source.
