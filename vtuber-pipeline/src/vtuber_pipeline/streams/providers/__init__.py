"""External clients used by the streams subapp."""

from vtuber_pipeline.streams.providers.gateway import (
    GatewayClient,
    GatewayError,
    GatewaySessionOpenResult,
    HTTPGatewayClient,
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
    "EgressAdminClient",
    "EgressAdminError",
    "EgressRegistration",
    "GatewayClient",
    "GatewayError",
    "GatewaySessionOpenResult",
    "HTTPEgressAdminClient",
    "HTTPGatewayClient",
    "MockYouTubeBinder",
    "NoneYouTubeBinder",
    "YouTubeBinder",
    "YouTubeBroadcast",
]
