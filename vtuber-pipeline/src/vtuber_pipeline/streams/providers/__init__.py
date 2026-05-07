"""External clients used by the streams subapp."""

from vtuber_pipeline.streams.providers.gateway import (
    BridgeClient,
    BridgeError,
    BridgeSessionOpenResult,
    HTTPBridgeClient,
)
from vtuber_pipeline.streams.providers.egress_admin import (
    EgressAdminClient,
    EgressAdminError,
    EgressRegistration,
    HTTPEgressAdminClient,
)
from vtuber_pipeline.streams.providers.youtube import (
    MockYouTubeBinder,
    NoneYouTubeBinder,
    YouTubeBinder,
    YouTubeBroadcast,
)

__all__ = [
    "BridgeClient",
    "BridgeError",
    "BridgeSessionOpenResult",
    "EgressAdminClient",
    "EgressAdminError",
    "EgressRegistration",
    "HTTPBridgeClient",
    "HTTPEgressAdminClient",
    "MockYouTubeBinder",
    "NoneYouTubeBinder",
    "YouTubeBinder",
    "YouTubeBroadcast",
]
