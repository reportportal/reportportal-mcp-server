# OAuth integration — ReportPortal backend side

Companion to `oauth-plan.md`, which covers the MCP server. This document covers what is needed
from `service-api` and the RP instance for the customer using Entra ID.

## Already supported (no work)

`service-api` validates JWTs from external IdPs via `rp.oauth2.providers.<name>.*`
(issuer-based routing + JWKS). Scopes in the token are ignored, `audience` is not validated,
roles fall back to the RP database when absent from the token, and the existing API-key path
keeps working in parallel as a fallback. Users must already exist in RP — there is no JIT
provisioning, which is fine since SCIM provisions them.

## Instance configuration

Add an Entra provider:

```properties
rp.oauth2.providers.entra.issuer-uri=<exact iss value from the token>
rp.oauth2.providers.entra.jwk-set-uri=https://login.microsoftonline.com/{tenant-id}/discovery/v2.0/keys
rp.oauth2.providers.entra.username-claim=<claim carrying the user identifier>
rp.oauth2.providers.entra.user-resolver=<DEFAULT|EXTERNAL>
```

Watch out for the Entra token version: v1 tokens are issued with
`https://sts.windows.net/{tenant-id}/` and v2 with `https://login.microsoftonline.com/{tenant-id}/v2.0`.
RP compares the issuer character by character, so a version mismatch silently breaks auth.

## Open question — how users are matched

RP resolves the user from the token in one of two ways:

- `user-resolver=DEFAULT` → looks up `users.login`
- `user-resolver=EXTERNAL` → looks up `users.external_id`

Entra emits `sub` (unique per application, unusable for matching), `oid` (tenant-wide GUID),
`preferred_username`, and optionally `email`/`upn`. Directory attributes such as `employeeId`
can also be emitted, but for **access tokens** that requires a claims mapping policy plus
`acceptMappedClaims=true` in the app manifest or a custom signing key — extra setup that
usually needs the customer's security approval.

Observed on the customer's staging: `external_id` is not uniform — some users have an employee
ID (`U423203`), others have the email prefix, which also equals their RP login.

**Root cause found in `scim-bridge`:** the bridge does zero transformation on this field — it
passes the SCIM `externalId` attribute straight through to `service-api`, with no fallback to
SCIM `id`, `userName`, or enterprise-extension attributes:

```19:28:/Users/Ilya_Hancharyk/Work/ReportPortal/AI capabilities/scim-bridge/internal/data/repository/user_converter.go
func toCreationParams(attr model.UserAttributes) client.UserDetails {
	return client.UserDetails{
		Email:        attr.GetEmail(),
		ExternalID:   attr.ExternalID,
		FullName:     attr.Name,
		AccountType:  "SCIM",
		InstanceRole: attr.InstanceRole.String(),
		Active:       true,
	}
}
```

So `users.external_id` is **exactly** whatever Entra sends as SCIM `externalId` — which is itself
just whatever source attribute the customer mapped to it under Provisioning → Attribute mapping.
The observed inconsistency is almost certainly because that mapping was changed at some point
(e.g. from `objectId` to `employeeId`) — old records reflect the old mapping, new/updated records
reflect the new one.

Also relevant: the bridge **never sends `login`** to `service-api` at all (only `email`, derived
from SCIM `userName`, lowercased; `emails[]` is ignored). So `login` is populated by `service-api`
itself, independent of Entra — another reason to prefer matching on `email` over `login`.

**To resolve, in this order:**

1. Ask the customer for the current source attribute behind `externalId` in Entra's Attribute
   Mapping screen — this is now known to be authoritative, no further verification needed.
2. Confirm that same attribute can be emitted as an access-token claim, and at what cost (see
   claims mapping policy caveat below).
3. If the mapping has changed historically and old users still hold stale values, decide whether
   to bulk re-sync `external_id` via SCIM before go-live, or use `email` matching instead to
   sidestep the inconsistency entirely.

**Best case:** if the current mapping is Entra's `objectId` → `externalId`, then
`username-claim=oid` plus `user-resolver=EXTERNAL` works with no code change and no custom claims
on the customer side (`oid` is a standard claim, emitted by default).

## Fallback if nothing lines up

If neither `login` nor `external_id` can be matched to a claim the customer is able to emit,
add a third resolver that matches on email: an `EMAIL` value in `UserResolverType`, a details
service built on the existing `userRepository.findByEmail`, and a branch in
`MultiIdentityProviderConfig.createJwtConverter`. Roughly half a day including tests.

Email is the only identifier that is uniform across all users and available as a standard Entra
claim, so this is the safe fallback — but it is a fallback, not the default choice.

## Timeline note

Configuration is per-instance and quick. A `service-api` code change is not: it has to land in a
ReportPortal release and be rolled out to the customer's SaaS instance, and that cycle may well
be longer than the entire MCP server implementation.
