---
trigger: always_on
---

1. Always use our internal logging module, and don't import another one (e.g zerolog)
2. Always use the hasura graphql client
3. Don't reimplement rate limiting, we have a module for that