# loop

The request-error listener seam: the loop's model-failure policy as a
swappable contract. See `loop.go` for the full contract.

## What the model sees

- **The default policy, unchanged**: one retry on the fallback model, latch
  on success; terminal otherwise. Behavior-identical to the inline fallback
  it replaced.
- **Failure shapes:** Terminal / RetryOnModel / RetryWithBackoff — the
  composition's listener decides; the loop only obeys.
