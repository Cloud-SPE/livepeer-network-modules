"""pipeline.streams — customer-facing streams orchestration.

Implements `pipeline-streams-api` plan: customers POST /api/streams once;
this subapp registers the egress session, opens the vtuber session via
the bridge, optionally creates a YouTube broadcast, and proxies chat +
events.
"""
