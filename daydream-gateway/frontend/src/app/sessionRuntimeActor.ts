import { fromCallback } from 'xstate'
import type { InitialParameters, StreamMode } from '../types'
import { closeGatewaySession, createWebRtcOffer, fetchIceServers, patchIceCandidate } from './scope'
import type { DiagnosticsState } from './studioTypes'

export type SessionChildInboundEvent =
  | { type: 'PROMPT_UPDATE'; payload: Record<string, unknown> }
  | { type: 'PROMPT_TRANSITION'; payload: Record<string, unknown> }
  | { type: 'RUNTIME_UPDATE'; payload: Record<string, unknown> }

export type SessionChildOutboundEvent =
  | { type: 'SESSION_LOG'; label: string; payload: unknown }
  | { type: 'SESSION_LABEL'; label: string }
  | { type: 'SESSION_ID_READY'; sessionId: string }
  | { type: 'LOCAL_STREAM_READY'; stream: MediaStream }
  | { type: 'REMOTE_STREAM_READY'; stream: MediaStream }
  | { type: 'PEER_CONNECTION_READY'; peerConnection: RTCPeerConnection }
  | { type: 'DATA_CHANNEL_READY'; dataChannel: RTCDataChannel }
  | { type: 'DIAGNOSTICS_PATCH'; patch: Partial<DiagnosticsState> }
  | { type: 'SESSION_CONNECTED' }
  | { type: 'SESSION_STREAMING' }
  | { type: 'SERVER_PIPELINE_ERROR'; message: string; fatal: boolean }
  | { type: 'SERVER_STREAM_STOPPED'; message?: string; fatal: boolean }
  | { type: 'SESSION_FAILED'; message: string }

export const sessionRuntimeActor = fromCallback<
  SessionChildOutboundEvent,
  {
    baseUrl: string
    mode: StreamMode
    initialParameters: InitialParameters
    localStream: MediaStream | null
  },
  SessionChildInboundEvent
>(({ input, sendBack, receive }) => {
  let peerConnection: RTCPeerConnection | null = null
  let dataChannel: RTCDataChannel | null = null
  let sessionId: string | null = null
  let localStream = input.localStream
  const queuedCandidates: RTCIceCandidate[] = []
  let statsIntervalId: number | null = null
  let lastInboundBytes = 0

  const log = (label: string, payload: unknown) => {
    sendBack({ type: 'SESSION_LOG', label, payload })
  }

  const patchDiagnostics = (patch: Partial<DiagnosticsState>) => {
    sendBack({ type: 'DIAGNOSTICS_PATCH', patch })
  }

  const cleanup = () => {
    if (statsIntervalId) {
      clearInterval(statsIntervalId)
      statsIntervalId = null
    }

    if (dataChannel) {
      dataChannel.close()
      dataChannel = null
    }

    if (peerConnection) {
      peerConnection.close()
      peerConnection = null
    }

    void closeGatewaySession(input.baseUrl)
  }

  receive((rawEvent: SessionChildInboundEvent | { type: string; payload?: unknown }) => {
    const event = rawEvent as SessionChildInboundEvent
    if (!dataChannel || dataChannel.readyState !== 'open') {
      log('data channel', 'Data channel is not open.')
      return
    }

    if (event.type === 'PROMPT_UPDATE' || event.type === 'PROMPT_TRANSITION' || event.type === 'RUNTIME_UPDATE') {
      dataChannel.send(JSON.stringify(event.payload))
      log(
        event.type === 'PROMPT_UPDATE'
          ? 'prompt update'
          : event.type === 'PROMPT_TRANSITION'
            ? 'prompt transition'
            : 'runtime update',
        event.payload,
      )
    }
  })

  ;(async () => {
    try {
      log('stream config', {
        pipelineId: input.initialParameters.pipeline_ids[0],
        mode: input.mode,
        initialParameters: input.initialParameters,
      })

      if (input.mode === 'webcam' && !localStream) {
        localStream = await navigator.mediaDevices.getUserMedia({ video: true, audio: false })
        sendBack({ type: 'LOCAL_STREAM_READY', stream: localStream })
        log('local input', 'Webcam stream ready.')
      }

      sendBack({ type: 'SESSION_LABEL', label: 'fetching ice' })
      const iceResponse = await fetchIceServers(input.baseUrl)
      log('ice servers', iceResponse.body)

      peerConnection = new RTCPeerConnection({
        iceServers: iceResponse.body.iceServers || [],
      })
      sendBack({ type: 'PEER_CONNECTION_READY', peerConnection })

      dataChannel = peerConnection.createDataChannel('parameters', { ordered: true })
      dataChannel.onopen = () => {
        sendBack({ type: 'DATA_CHANNEL_READY', dataChannel: dataChannel as RTCDataChannel })
        log('data channel', 'Open. Ready for live updates.')
      }

      dataChannel.onmessage = (event) => {
        let parsed: unknown = event.data

        try {
          parsed = JSON.parse(event.data)
        } catch {
          // keep raw payload
        }

        if ((parsed as { type?: string })?.type !== 'tempo_update') {
          log('data channel message', parsed)
        }

        if ((parsed as { type?: string; message?: string; fatal?: boolean })?.type === 'pipeline_error') {
          sendBack({
            type: 'SERVER_PIPELINE_ERROR',
            message: (parsed as { message?: string }).message || 'Pipeline error reported by Scope.',
            fatal: Boolean((parsed as { fatal?: boolean }).fatal),
          })
        }

        if ((parsed as { type?: string })?.type === 'stream_stopped') {
          sendBack({
            type: 'SERVER_STREAM_STOPPED',
            message: (parsed as { error_message?: string }).error_message,
            fatal: Boolean((parsed as { fatal?: boolean }).fatal),
          })
        }
      }

      dataChannel.onclose = () => {
        log('data channel', 'Closed.')
      }

      if (input.mode === 'webcam' && localStream) {
        log('local input', 'Adding webcam track to peer connection.')
        localStream.getTracks().forEach((track) => {
          if (track.kind === 'video') {
            peerConnection?.addTrack(track, localStream as MediaStream)
          }
        })
      } else {
        log('local input', 'Receive-only mode: requesting remote video with transceiver only.')
        peerConnection.addTransceiver('video')
      }

      peerConnection.ontrack = (event) => {
        const stream = event.streams?.[0]
        if (stream) {
          sendBack({ type: 'REMOTE_STREAM_READY', stream })
          patchDiagnostics({ track: 'attached' })
          sendBack({ type: 'SESSION_CONNECTED' })
          log('webrtc', 'Remote video track attached.')
        }

        const [track] = stream?.getVideoTracks?.() || []
        if (track) {
          track.addEventListener('ended', () => {
            patchDiagnostics({ track: 'ended', frames: 'track ended' })
            log('track', 'Remote video track ended.')
          })

          track.addEventListener('mute', () => {
            patchDiagnostics({ track: 'muted' })
            log('track', 'Remote video track muted.')
          })

          track.addEventListener('unmute', () => {
            patchDiagnostics({ track: 'unmuted' })
            log('track', 'Remote video track unmuted.')
          })
        }
      }

      peerConnection.onconnectionstatechange = () => {
        patchDiagnostics({ connection: peerConnection?.connectionState || 'unknown' })
        sendBack({
          type: peerConnection?.connectionState === 'failed' ? 'SESSION_FAILED' : 'SESSION_LABEL',
          ...(peerConnection?.connectionState === 'failed'
            ? { message: 'Peer connection failed.' }
            : { label: `pc ${peerConnection?.connectionState}` }),
        } as SessionChildOutboundEvent)
        log('connection state', peerConnection?.connectionState || 'unknown')
      }

      peerConnection.oniceconnectionstatechange = () => {
        patchDiagnostics({ ice: peerConnection?.iceConnectionState || 'unknown' })
        log('ice state', peerConnection?.iceConnectionState || 'unknown')
      }

      peerConnection.onsignalingstatechange = () => {
        patchDiagnostics({ signaling: peerConnection?.signalingState || 'unknown' })
        log('signaling state', peerConnection?.signalingState || 'unknown')
      }

      peerConnection.onicecandidate = async (event) => {
        if (!event.candidate) {
          return
        }

        if (sessionId) {
          await patchIceCandidate(input.baseUrl, sessionId, event.candidate)
        } else {
          queuedCandidates.push(event.candidate)
        }
      }

      sendBack({ type: 'SESSION_LABEL', label: 'creating offer' })
      const offer = await peerConnection.createOffer()
      await peerConnection.setLocalDescription(offer)

      const answer = await createWebRtcOffer(input.baseUrl, {
        sdp: peerConnection.localDescription?.sdp,
        type: peerConnection.localDescription?.type,
        initialParameters: input.initialParameters,
      })
      log('webrtc offer', answer.body)

      sessionId = answer.body.sessionId
      sendBack({ type: 'SESSION_ID_READY', sessionId })
      patchDiagnostics({ session: sessionId })

      await peerConnection.setRemoteDescription({
        type: answer.body.type,
        sdp: answer.body.sdp,
      })

      for (const candidate of queuedCandidates) {
        await patchIceCandidate(input.baseUrl, sessionId, candidate)
      }

      queuedCandidates.length = 0
      sendBack({ type: 'SESSION_LABEL', label: 'awaiting media' })

      statsIntervalId = window.setInterval(async () => {
        if (!peerConnection) {
          return
        }

        try {
          const stats = await peerConnection.getStats()
          const inbound = [...stats.values()].find(
            (report) => report.type === 'inbound-rtp' && report.kind === 'video',
          )
          if (!inbound) {
            return
          }

          const bytesReceived = inbound.bytesReceived || 0
          const framesDecoded = inbound.framesDecoded || 0
          const framesReceived = inbound.framesReceived || 0
          const frameWidth = inbound.frameWidth || 0
          const frameHeight = inbound.frameHeight || 0

          patchDiagnostics({
            bytes: bytesReceived.toLocaleString(),
            decoded: `${framesDecoded}/${framesReceived}`,
            video: frameWidth > 0 && frameHeight > 0 ? `${frameWidth} x ${frameHeight}` : undefined,
          })

          if (bytesReceived > lastInboundBytes && framesDecoded > 0) {
            patchDiagnostics({ frames: 'flowing' })
            sendBack({ type: 'SESSION_STREAMING' })
          } else if (bytesReceived > lastInboundBytes) {
            patchDiagnostics({ frames: 'bytes only' })
            sendBack({ type: 'SESSION_LABEL', label: 'receiving undecoded media' })
          } else if (bytesReceived === 0 && peerConnection.connectionState === 'connected') {
            patchDiagnostics({ frames: 'no media yet' })
          }

          lastInboundBytes = bytesReceived
        } catch (error) {
          log('stats error', error instanceof Error ? error.message : String(error))
        }
      }, 1000)
    } catch (error) {
      sendBack({
        type: 'SESSION_FAILED',
        message: error instanceof Error ? error.message : String(error),
      })
    }
  })()

  return cleanup
})
