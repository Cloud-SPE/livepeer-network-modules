"""Mock YouTube Live Streaming API.

A minimal stand-in for the four endpoints Pipeline calls during stream
setup (`liveBroadcasts.insert`, `liveStreams.insert`, `liveBroadcasts.bind`,
`liveBroadcasts.transition`) plus a small dashboard page showing active
broadcasts. Returns well-formed fakes; never talks to YouTube.

See docs/exec-plans/active/mock-youtube-egress.md.
"""

__version__ = "0.0.0"
