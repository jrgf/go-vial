# Security policy

## Supported versions

Until Vial 1.0, security fixes are released for the latest published minor
version only. After 1.0, the latest 1.x minor receives fixes; an older minor
may receive a backport when upgrading immediately would create material risk.
Unsupported versions should be upgraded before requesting a patch.

## Report a vulnerability

Do not open a public issue. Use GitHub's private vulnerability reporting form:
https://github.com/jrgf/go-vial/security/advisories/new. This private advisory
is the security contact for the project maintainers. Include affected versions,
impact, reproduction steps, and any proposed mitigation; avoid real credentials
or data belonging to others.

## Response and disclosure

Maintainers aim to acknowledge a report within three business days and provide
an initial severity assessment within seven. The reporter and maintainers will
coordinate a disclosure date after a fix and supported-version guidance are
ready. A release advisory will credit the reporter unless anonymity is
requested. If a report is already public or actively exploited, maintainers may
publish mitigations earlier to protect users.
