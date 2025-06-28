---
trigger: model_decision
description: Any time we're doing something with the hardcover API
---

1. Hardcover's API has a pretty strict rate limiting in place (currently: 60 requests per minute). so use the rate limit module we have
2. Queries have a max timeout of 30 seconds.
3. Most "user" APIs need to be filtered for the current user when queried. Write operations are user-scoped as far as I can tell
4. The following queries are disabled:
  - _like
  - _nlike
  - _ilike
  - _niregex
  - _nregex
  - _iregex
  - _regex
  - _nsimilar
  - _similar
