import { sendParent, sendTo, setup } from 'xstate'
import type { InitialParameters, StreamMode } from '../types'
import { sessionRuntimeActor, type SessionChildOutboundEvent } from './sessionRuntimeActor'
import type { SessionPhase } from './studioTypes'

export type SessionCommandEvent =
  | {
      type: 'START'
      baseUrl: string
      mode: StreamMode
      initialParameters: InitialParameters
      localStream: MediaStream | null
    }
  | { type: 'STOP' }
  | { type: 'PROMPT_UPDATE'; payload: Record<string, unknown> }
  | { type: 'PROMPT_TRANSITION'; payload: Record<string, unknown> }
  | { type: 'RUNTIME_UPDATE'; payload: Record<string, unknown> }

export type SessionParentEvent =
  | { type: 'SESSION_STATUS'; phase: SessionPhase; label: string }
  | SessionChildOutboundEvent

export const sessionMachine = setup({
  types: {
    events: {} as SessionCommandEvent | SessionChildOutboundEvent,
  },
  actors: {
    sessionRuntimeActor,
  },
}).createMachine({
  id: 'session',
  initial: 'idle',
  states: {
    idle: {
      entry: sendParent({
        type: 'SESSION_STATUS',
        phase: 'idle',
        label: 'idle',
      }),
      on: {
        START: 'running',
      },
    },
    running: {
      entry: sendParent({
        type: 'SESSION_STATUS',
        phase: 'creatingOffer',
        label: 'starting session',
      }),
      invoke: {
        id: 'runtime',
        src: 'sessionRuntimeActor',
        input: ({ event }) => {
          const command = event as Extract<SessionCommandEvent, { type: 'START' }>
          return {
            baseUrl: command.baseUrl,
            mode: command.mode,
            initialParameters: command.initialParameters,
            localStream: command.localStream,
          }
        },
      },
      on: {
        STOP: 'idle',
        PROMPT_UPDATE: {
          actions: sendTo('runtime', ({ event }) => event),
        },
        PROMPT_TRANSITION: {
          actions: sendTo('runtime', ({ event }) => event),
        },
        RUNTIME_UPDATE: {
          actions: sendTo('runtime', ({ event }) => event),
        },
        SESSION_LOG: {
          actions: sendParent(({ event }) => event),
        },
        SESSION_LABEL: {
          actions: sendParent(({ event }) => ({
            type: 'SESSION_STATUS',
            phase:
              event.label === 'fetching ice'
                ? 'fetchingIce'
                : event.label === 'creating offer'
                  ? 'creatingOffer'
                  : event.label === 'awaiting media'
                    ? 'awaitingMedia'
                    : event.label === 'receiving undecoded media'
                      ? 'connected'
                      : event.label.startsWith('pc ')
                        ? 'connected'
                        : 'connected',
            label: event.label,
          })),
        },
        SESSION_ID_READY: {
          actions: sendParent(({ event }) => event),
        },
        LOCAL_STREAM_READY: {
          actions: sendParent(({ event }) => event),
        },
        REMOTE_STREAM_READY: {
          actions: sendParent(({ event }) => event),
        },
        PEER_CONNECTION_READY: {
          actions: sendParent(({ event }) => event),
        },
        DATA_CHANNEL_READY: {
          actions: sendParent(({ event }) => event),
        },
        DIAGNOSTICS_PATCH: {
          actions: sendParent(({ event }) => event),
        },
        SESSION_CONNECTED: {
          actions: [
            sendParent({
              type: 'SESSION_STATUS',
              phase: 'connected',
              label: 'video track received',
            }),
            sendParent(({ event }) => event),
          ],
        },
        SESSION_STREAMING: {
          actions: [
            sendParent({
              type: 'SESSION_STATUS',
              phase: 'streaming',
              label: 'streaming frames',
            }),
            sendParent(({ event }) => event),
          ],
        },
        SERVER_PIPELINE_ERROR: {
          actions: sendParent(({ event }) => event),
        },
        SERVER_STREAM_STOPPED: {
          target: 'idle',
          actions: [
            sendParent(({ event }) => ({
              type: 'SESSION_STATUS',
              phase: event.fatal ? 'serverError' : 'stopped',
              label: event.fatal ? 'server error' : 'stopped by server',
            })),
            sendParent(({ event }) => event),
          ],
        },
        SESSION_FAILED: {
          target: 'idle',
          actions: [
            sendParent({
              type: 'SESSION_STATUS',
              phase: 'failed',
              label: 'failed',
            }),
            sendParent(({ event }) => event),
          ],
        },
      },
    },
  },
})
