# Pool implementation utilities

Optional Go utilities used by the regional pool implementations. This module
contains no accounting authority and no network protocol requirement. Keep its
packages independent of component internals. Authentication must fail closed;
never log credentials or disable TLS certificate verification. No workload
capability enums or policy decisions belong here. Root AGENTS.md applies.

`make test` and `make build` run in Docker. Contract and failure-path tests are
required for changes to authorization and transfer fencing.
