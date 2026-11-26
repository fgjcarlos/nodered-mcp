# Issue #446 — Node-RED admin authentication
Source: https://github.com/fgjcarlos/nodered-mcp/issues/446. Branch: `fix/issue-446-admin-auth`.

## Specs
- S1: Replace `basicAuth` with an authenticator that does `POST /auth/token` (`client_id=node-red-admin`, `grant_type=password`, `scope=*`) and uses the `access_token` as Bearer.
- S2: Keep the token in memory and request a new one on a 401 or when `expires_in` is reached (a single retry).
- S3: If `GET /auth/login` reports that there is no login, send no credentials.
- S4: Document in the README that username and password is the recommended option and that `NODERED_TOKEN` expires.
- S5: With a valid username and password, every tool works against a Node-RED with `adminAuth`.
- S6: An expired token is renewed automatically, without restarting the MCP.
- S7: Wrong credentials give a clear error and are not retried in a loop.
- S8: No log or error message contains the password or the token.

## Tasks
- T1 [done] S1–S8 — Inline password authenticator, integration, verification and docs; implementation `46f8fff`, scoped review correction `11754de`.

## Log
- L1: Resuelve la issue #446
- L2: Branch started from `origin/main` after PR #466 merged. The issue reports HTTP 401 against Node-RED 5.0.9 with `adminAuth` enabled.
- L3: RED reproduced missing password grant. The independent check and first isolated Node-RED 5.0.1 smoke found that invalid passwords return 403, not only 401. Both now stop each authentication attempt and tail retry loop; the regression and corrected smoke passed. The smoke called `list_flows` twice on the same process across a two-second token lifetime, then checked wrong credentials in a separate process. Other tools were tested with fixtures, not live.
- L4: The 656-line implementation commit includes the shared HTTP/WebSocket credentials path, its tests and docs as one behavior slice. Native review `review-8626c481488c46ae` required a redirect-compatibility correction. The second commit follows same-origin redirects and the anonymous same-host TLS upgrade; it prevents credential forwarding on cross-origin or downgrade redirects. The patch was 112 diff lines within the approved 180-line plan.
- L5: Focused RED/GREEN for the proxy case; full Go suite, vet, formatting, complexity, targeted race, baseline comparison of six existing paths, and independent checks passed. Native review was approved and acknowledged after the correction. No push or PR.
