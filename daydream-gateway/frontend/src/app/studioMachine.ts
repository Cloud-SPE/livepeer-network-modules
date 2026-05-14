import { assign, sendTo, setup } from 'xstate'
import type { PromptInterpolation, StreamMode } from '../types'
import {
  appendLog,
  applyLook,
  buildDefaultLoadValues,
  buildDefaultRuntimeValues,
  defaultDiagnostics,
  defaultMacros,
  defaultModelStatus,
  defaultPipelineStatus,
  defaultRuntimeValues,
  getInitialParameters,
  getPromptPayload,
} from './studioState'
import { editorMachine, type EditorParentEvent } from './editorMachine'
import { resourcesMachine, type ResourcesParentEvent } from './resourcesMachine'
import { sessionMachine } from './sessionMachine'
import { setupMachine, type SetupParentEvent } from './setupMachine'
import {
  defaultBaseUrl,
  defaultPrompt,
  fallbackPipelineId,
  type StudioContext,
  type MacroState,
  type RuntimeValues,
} from './studioTypes'
import type { SessionParentEvent } from './sessionMachine'

export type StudioEvent =
  | { type: 'BOOT' }
  | { type: 'REFRESH_REQUEST' }
  | { type: 'SET_BASE_URL'; value: string }
  | { type: 'SELECT_PIPELINE'; pipelineId: string }
  | { type: 'SET_MODE'; mode: StreamMode }
  | { type: 'SET_PROMPT'; value: string }
  | { type: 'SET_PROMPT_INTERPOLATION'; value: PromptInterpolation }
  | { type: 'SET_TRANSITION_STEPS'; value: number }
  | { type: 'SELECT_LOOK'; lookId: string }
  | { type: 'SET_MACRO'; key: keyof MacroState; value: number }
  | { type: 'SET_LOAD_CONTROL_MODE'; value: 'basic' | 'advanced' }
  | { type: 'SET_RUNTIME_CONTROL_MODE'; value: 'basic' | 'advanced' }
  | { type: 'SET_LOAD_VALUE'; key: string; value: unknown }
  | { type: 'SET_RUNTIME_VALUE'; key: keyof RuntimeValues; value: unknown }
  | { type: 'APPLY_LOAD_PRESET'; width: number; height: number }
  | { type: 'APPLY_RUNTIME_PRESET'; values: number[] }
  | { type: 'DOWNLOAD_MODELS' }
  | { type: 'LOAD_PIPELINE' }
  | { type: 'TOGGLE_CAMERA' }
  | { type: 'START_SESSION' }
  | { type: 'STOP_SESSION' }
  | { type: 'SEND_PROMPT_UPDATE' }
  | { type: 'SEND_PROMPT_TRANSITION' }
  | { type: 'SEND_RUNTIME_UPDATE' }
  | { type: 'CLEAR_LOGS' }
  | { type: 'CLEAR_BANNER' }
  | EditorParentEvent
  | SessionParentEvent
  | SetupParentEvent
  | ResourcesParentEvent

const initialContext: StudioContext = {
  baseUrl: defaultBaseUrl,
  availablePipelines: [fallbackPipelineId],
  pipelineSchemas: {},
  selectedPipelineId: fallbackPipelineId,
  mode: 'receive',
  promptBase: defaultPrompt,
  promptInterpolation: 'linear',
  transitionSteps: 8,
  selectedLookId: 'cinematic',
  macros: defaultMacros,
  loadValues: {},
  runtimeValues: defaultRuntimeValues,
  loadControlMode: 'basic',
  runtimeControlMode: 'basic',
  pipelineStatus: defaultPipelineStatus(),
  modelStatus: defaultModelStatus(),
  banner: null,
  logs: ['Ready.'],
  diagnostics: defaultDiagnostics(),
  sessionLabel: 'idle',
  sessionId: null,
  peerConnection: null,
  dataChannel: null,
  localStream: null,
  remoteStream: null,
  pendingSessionStart: false,
  resourceActivity: 'idle',
  sessionPhase: 'idle',
}

function buildEditorSyncEvent(context: StudioContext) {
  return {
    type: 'EDITOR_SYNC' as const,
    pipelineSchemas: context.pipelineSchemas,
    selectedPipelineId: context.selectedPipelineId,
    mode: context.mode,
    promptBase: context.promptBase,
    promptInterpolation: context.promptInterpolation,
    transitionSteps: context.transitionSteps,
    selectedLookId: context.selectedLookId,
    macros: context.macros,
    loadValues: context.loadValues,
    runtimeValues: context.runtimeValues,
    loadControlMode: context.loadControlMode,
    runtimeControlMode: context.runtimeControlMode,
  }
}

function buildContextFromSetupPatch(
  context: StudioContext,
  patch: SetupParentEvent['patch'],
): StudioContext {
  const nextBase = {
    ...context,
    ...patch,
  }

  if (
    patch.pipelineSchemas === undefined &&
    patch.selectedPipelineId === undefined &&
    patch.mode === undefined
  ) {
    return nextBase
  }

  const schema = nextBase.pipelineSchemas[nextBase.selectedPipelineId] || null
  const editorSeed = {
    ...nextBase,
    loadValues: buildDefaultLoadValues(schema, nextBase.mode),
    runtimeValues: buildDefaultRuntimeValues(schema, nextBase.mode),
    localStream: patch.mode === 'webcam' ? nextBase.localStream : null,
  }

  return {
    ...editorSeed,
    ...applyLook(editorSeed),
  }
}

export const studioMachine = setup({
  types: {
    context: {} as StudioContext,
    events: {} as StudioEvent,
  },
  actors: {
    editorMachine,
    resourcesMachine,
    sessionMachine,
    setupMachine,
  },
  guards: {
    modelsReady: ({ context }) => context.modelStatus.downloaded,
    canDownloadModels: ({ context }) =>
      !context.modelStatus.downloaded && !context.modelStatus.downloading,
    canLoadPipeline: ({ context }) => context.modelStatus.downloaded,
    canStartSession: ({ context }) => context.modelStatus.downloaded,
    needsCamera: ({ context }) => context.mode === 'webcam' && !context.localStream,
  },
}).createMachine({
  id: 'studio',
  initial: 'booting',
  context: initialContext,
  on: {
    CLEAR_LOGS: {
      actions: assign({
        logs: ['Ready.'],
      }),
    },
    EDITOR_PATCH: {
      actions: assign(({ event }) => event.patch),
    },
    SETUP_PATCH: {
      actions: [
        assign(({ context, event }) => buildContextFromSetupPatch(context, event.patch)),
        sendTo('editor', ({ context, event }) =>
          buildEditorSyncEvent(buildContextFromSetupPatch(context, event.patch)),
        ),
      ],
    },
    RESOURCES_STATUS: {
      actions: assign({
        resourceActivity: ({ event }) => event.activity,
      }),
    },
    SESSION_STATUS: {
      actions: assign({
        sessionPhase: ({ event }) => event.phase,
        sessionLabel: ({ event }) => event.label,
      }),
    },
  },
  invoke: [
    {
      id: 'editor',
      src: 'editorMachine',
    },
    {
      id: 'setup',
      src: 'setupMachine',
    },
    {
      id: 'resources',
      src: 'resourcesMachine',
    },
    {
      id: 'session',
      src: 'sessionMachine',
    },
  ],
  states: {
    booting: {
      entry: sendTo('resources', ({ context }) => ({
        type: 'BOOTSTRAP',
        baseUrl: context.baseUrl,
        selectedPipelineId: context.selectedPipelineId,
        mode: context.mode,
      })),
      on: {
        RESOURCES_BOOTSTRAPPED: {
          target: 'ready',
          actions: [
            assign(({ context, event }) => ({
              pipelineStatus: event.output.snapshot.pipelineStatusUi,
              modelStatus: event.output.snapshot.modelStatusUi,
              logs: appendLog(
                appendLog(context.logs, 'pipeline schemas (200)', event.output.schemas),
                'pipelines discovered',
                Object.keys(event.output.schemas.pipelines || {}),
              ),
            })),
            sendTo('setup', ({ context, event }) => ({
              type: 'SETUP_BOOTSTRAP',
              baseUrl: context.baseUrl,
              schemas: event.output.schemas,
              selectedPipelineId: context.selectedPipelineId,
              mode: context.mode,
            })),
          ],
        },
        RESOURCES_ERROR: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            banner: {
              message: event.error instanceof Error ? event.error.message : String(event.error),
              tone: 'error',
            },
            logs: appendLog(context.logs, 'initial load failed', event.error),
          })),
        },
      },
    },
    ready: {
      on: {
        BOOT: {
          target: 'booting',
          actions: assign(() => initialContext),
        },
        REFRESH_REQUEST: {
          target: 'refreshing',
        },
        SET_BASE_URL: {
          actions: sendTo('setup', ({ event }) => event),
        },
        SELECT_PIPELINE: {
          actions: sendTo('setup', ({ event }) => event),
        },
        SET_MODE: {
          actions: [
            assign(({ context, event }) => {
              if (event.mode !== 'webcam' && context.localStream) {
                context.localStream.getTracks().forEach((track) => track.stop())
                return { localStream: null }
              }
              return {}
            }),
            sendTo('setup', ({ event }) => event),
          ],
        },
        SET_PROMPT: { actions: sendTo('editor', ({ event }) => event) },
        SET_PROMPT_INTERPOLATION: { actions: sendTo('editor', ({ event }) => event) },
        SET_TRANSITION_STEPS: { actions: sendTo('editor', ({ event }) => event) },
        SELECT_LOOK: { actions: sendTo('editor', ({ event }) => event) },
        SET_MACRO: { actions: sendTo('editor', ({ event }) => event) },
        SET_LOAD_CONTROL_MODE: {
          actions: [
            assign({
              loadControlMode: ({ event }) => event.value,
            }),
            sendTo('editor', ({ event }) => event),
          ],
        },
        SET_RUNTIME_CONTROL_MODE: {
          actions: [
            assign({
              runtimeControlMode: ({ event }) => event.value,
            }),
            sendTo('editor', ({ event }) => event),
          ],
        },
        SET_LOAD_VALUE: { actions: sendTo('editor', ({ event }) => event) },
        SET_RUNTIME_VALUE: { actions: sendTo('editor', ({ event }) => event) },
        APPLY_LOAD_PRESET: { actions: sendTo('editor', ({ event }) => event) },
        APPLY_RUNTIME_PRESET: { actions: sendTo('editor', ({ event }) => event) },
        DOWNLOAD_MODELS: {
          guard: 'canDownloadModels',
          target: 'downloadingModels',
        },
        LOAD_PIPELINE: {
          guard: 'canLoadPipeline',
          target: 'loadingPipeline',
        },
        TOGGLE_CAMERA: [
          {
            guard: ({ context }) => Boolean(context.localStream),
            actions: assign(({ context }) => {
              context.localStream?.getTracks().forEach((track) => track.stop())
              return {
                localStream: null,
              }
            }),
          },
          {
            target: 'requestingCamera',
          },
        ],
        START_SESSION: [
          {
            guard: 'needsCamera',
            target: 'requestingCamera',
            actions: assign({
              pendingSessionStart: true,
              banner: null,
            }),
          },
          {
            guard: 'canStartSession',
            target: 'preparingSession',
            actions: assign({
              pendingSessionStart: true,
              banner: null,
              logs: ({ context }) =>
                appendLog(context.logs, 'stream config', {
                  pipelineId: context.selectedPipelineId,
                  mode: context.mode,
                  loadParams: context.loadValues,
                  initialParameters: getInitialParameters(context),
                }),
            }),
          },
        ],
        CLEAR_BANNER: {
          actions: assign({
            banner: null,
          }),
        },
      },
    },
    refreshing: {
      entry: sendTo('resources', ({ context }) => ({
        type: 'REFRESH',
        baseUrl: context.baseUrl,
        selectedPipelineId: context.selectedPipelineId,
      })),
      on: {
        RESOURCES_REFRESHED: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            pipelineStatus: event.output.pipelineStatusUi,
            modelStatus: event.output.modelStatusUi,
            logs: appendLog(
              appendLog(context.logs, 'pipeline status (200)', event.output.pipelineStatus),
              'models (200)',
              event.output.modelStatus,
            ),
          })),
        },
        RESOURCES_ERROR: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            banner: {
              message: event.error instanceof Error ? event.error.message : String(event.error),
              tone: 'error',
            },
            logs: appendLog(context.logs, 'refresh failed', event.error),
          })),
        },
      },
    },
    downloadingModels: {
      entry: sendTo('resources', ({ context }) => ({
        type: 'DOWNLOAD_MODELS',
        baseUrl: context.baseUrl,
        selectedPipelineId: context.selectedPipelineId,
      })),
      on: {
        RESOURCES_MODEL_DOWNLOAD_STARTED: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            modelStatus: {
              badge: 'downloading',
              stage:
                event.output.body.message ||
                `Downloading ${context.selectedPipelineId} models...`,
              downloaded: false,
              downloading: true,
            },
            logs: appendLog(context.logs, `download models (${event.output.status})`, event.output.body),
          })),
        },
        RESOURCES_ERROR: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            banner: {
              message: event.error instanceof Error ? event.error.message : String(event.error),
              tone: 'error',
            },
            logs: appendLog(context.logs, 'model download failed', event.error),
            modelStatus: {
              badge: 'error',
              stage: event.error instanceof Error ? event.error.message : String(event.error),
              downloaded: false,
              downloading: false,
            },
          })),
        },
      },
    },
    loadingPipeline: {
      entry: [
        assign({
          pipelineStatus: ({ context }) => ({
            ...context.pipelineStatus,
            badge: 'loading',
            stage: 'Loading selected pipeline...',
            loading: true,
          }),
        }),
        sendTo('resources', ({ context }) => ({
          type: 'LOAD_PIPELINE',
          baseUrl: context.baseUrl,
          selectedPipelineId: context.selectedPipelineId,
          loadValues: context.loadValues,
        })),
      ],
      on: {
        RESOURCES_PIPELINE_LOADED: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            pipelineStatus: event.output.pipelineStatusUi,
            modelStatus: event.output.modelStatusUi,
            logs: appendLog(context.logs, 'pipeline loaded', event.output.pipelineStatus),
          })),
        },
        RESOURCES_ERROR: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            banner: {
              message: event.error instanceof Error ? event.error.message : String(event.error),
              tone: 'error',
            },
            logs: appendLog(context.logs, 'pipeline load failed', event.error),
            pipelineStatus: {
              badge: 'error',
              stage: event.error instanceof Error ? event.error.message : String(event.error),
              loadedPipelineId: context.pipelineStatus.loadedPipelineId,
              loading: false,
            },
          })),
        },
      },
    },
    requestingCamera: {
      entry: [
        assign({
          sessionLabel: 'requesting camera',
        }),
        sendTo('resources', { type: 'REQUEST_CAMERA' }),
      ],
      on: {
        RESOURCES_CAMERA_READY: [
          {
            target: 'preparingSession',
            guard: ({ context }) => context.pendingSessionStart,
            actions: assign(({ context, event }) => ({
              localStream: event.stream,
              logs: appendLog(context.logs, 'local input', 'Webcam stream ready.'),
            })),
          },
          {
            target: 'ready',
            actions: assign(({ context, event }) => ({
              localStream: event.stream,
              sessionLabel: 'idle',
              logs: appendLog(context.logs, 'local input', 'Webcam stream ready.'),
            })),
          },
        ],
        RESOURCES_ERROR: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            banner: {
              message: event.error instanceof Error ? event.error.message : String(event.error),
              tone: 'error',
            },
            logs: appendLog(context.logs, 'local input error', event.error),
            pendingSessionStart: false,
            sessionLabel: 'idle',
          })),
        },
        BOOT: {
          target: 'booting',
          actions: assign(() => initialContext),
        },
      },
      always: {
        guard: ({ context }) => !context.pendingSessionStart && Boolean(context.localStream),
        target: 'ready',
      },
    },
    preparingSession: {
      entry: [
        assign({
          sessionLabel: 'loading pipeline',
        }),
        sendTo('resources', ({ context }) => ({
          type: 'PREPARE_SESSION',
          baseUrl: context.baseUrl,
          selectedPipelineId: context.selectedPipelineId,
          loadValues: context.loadValues,
          pipelineStatus: context.pipelineStatus,
        })),
      ],
      on: {
        RESOURCES_SESSION_PREPARED: {
          target: 'sessionActive',
          actions: assign(({ event }) => ({
            pipelineStatus: event.output.pipelineStatusUi,
            pendingSessionStart: false,
          })),
        },
        RESOURCES_ERROR: {
          target: 'ready',
          actions: assign(({ context, event }) => ({
            pendingSessionStart: false,
            banner: {
              message: event.error instanceof Error ? event.error.message : String(event.error),
              tone: 'error',
            },
            logs: appendLog(context.logs, 'session preparation failed', event.error),
            sessionLabel: 'idle',
          })),
        },
      },
    },
    sessionActive: {
      initial: 'starting',
      entry: sendTo('session', ({ context }) => ({
        type: 'START',
        baseUrl: context.baseUrl,
        mode: context.mode,
        initialParameters: getInitialParameters(context),
        localStream: context.localStream,
      })),
      states: {
        starting: {},
        connected: {},
        streaming: {},
      },
      on: {
        SESSION_LOG: {
          actions: assign({
            logs: ({ context, event }) => appendLog(context.logs, event.label, event.payload),
          }),
        },
        SESSION_ID_READY: {
          actions: assign({
            sessionId: ({ event }) => event.sessionId,
            diagnostics: ({ context, event }) => ({
              ...context.diagnostics,
              session: event.sessionId,
            }),
          }),
        },
        LOCAL_STREAM_READY: {
          actions: assign({
            localStream: ({ event }) => event.stream,
          }),
        },
        REMOTE_STREAM_READY: {
          target: '.connected',
          actions: assign({
            remoteStream: ({ event }) => event.stream,
            diagnostics: ({ context }) => ({
              ...context.diagnostics,
              track: 'attached',
            }),
          }),
        },
        PEER_CONNECTION_READY: {
          actions: assign({
            peerConnection: ({ event }) => event.peerConnection,
          }),
        },
        DATA_CHANNEL_READY: {
          target: '.connected',
          actions: assign({
            dataChannel: ({ event }) => event.dataChannel,
          }),
        },
        DIAGNOSTICS_PATCH: {
          actions: assign({
            diagnostics: ({ context, event }) => ({
              ...context.diagnostics,
              ...Object.fromEntries(
                Object.entries(event.patch).filter(([, value]) => value !== undefined),
              ),
            }),
          }),
        },
        SESSION_CONNECTED: {
          target: '.connected',
          actions: assign({
          }),
        },
        SESSION_STREAMING: {
          target: '.streaming',
        },
        SERVER_PIPELINE_ERROR: {
          actions: assign({
            banner: ({ event }) => ({
              message: event.message,
              tone: event.fatal ? 'error' : 'warning',
            }),
          }),
        },
        SERVER_STREAM_STOPPED: [
          {
            guard: ({ event }) => event.fatal,
            target: 'serverError',
            actions: assign(({ context, event }) => ({
              banner: {
                message: event.message || 'Scope stopped the stream.',
                tone: 'error',
              },
              diagnostics: {
                ...context.diagnostics,
                frames: 'server fatal stop',
              },
            })),
          },
          {
            target: 'ready',
            actions: assign(({ context, event }) => ({
              banner: {
                message: event.message || 'Scope stopped the stream.',
                tone: 'warning',
              },
              diagnostics: {
                ...context.diagnostics,
                frames: 'server stopped',
              },
              remoteStream: null,
              peerConnection: null,
              dataChannel: null,
              sessionId: null,
            })),
          },
        ],
        SESSION_FAILED: {
          target: 'failed',
          actions: assign(({ context, event }) => ({
            banner: {
              message: event.message,
              tone: 'error',
            },
            logs: appendLog(context.logs, 'session failed', event.message),
          })),
        },
        SEND_PROMPT_UPDATE: {
          actions: sendTo('session', ({ context }) => ({
            type: 'PROMPT_UPDATE',
            payload: {
              prompts: getPromptPayload(context),
              prompt_interpolation_method: context.promptInterpolation,
            },
          })),
        },
        SEND_PROMPT_TRANSITION: {
          actions: sendTo('session', ({ context }) => ({
            type: 'PROMPT_TRANSITION',
            payload: {
              transition: {
                target_prompts: getPromptPayload(context),
                num_steps: context.transitionSteps,
                temporal_interpolation_method: context.promptInterpolation,
              },
            },
          })),
        },
        SEND_RUNTIME_UPDATE: {
          actions: sendTo('session', ({ context }) => ({
            type: 'RUNTIME_UPDATE',
            payload: {
              denoising_step_list: context.runtimeValues.denoising_step_list,
              noise_scale: context.runtimeValues.noise_scale,
              noise_controller: context.runtimeValues.noise_controller,
              manage_cache: context.runtimeValues.manage_cache,
              reset_cache: context.runtimeValues.reset_cache,
              vace_context_scale: context.runtimeValues.vace_context_scale,
            },
          })),
        },
        STOP_SESSION: {
          target: 'ready',
          actions: [
            sendTo('session', { type: 'STOP' }),
            assign(({ context }) => ({
              remoteStream: null,
              peerConnection: null,
              dataChannel: null,
              sessionId: null,
              sessionPhase: 'idle',
              diagnostics: {
                ...defaultDiagnostics(),
                session: 'none',
              },
              logs: appendLog(context.logs, 'stream', 'Stopped local WebRTC session.'),
            })),
          ],
        },
      },
    },
    failed: {
      on: {
        STOP_SESSION: {
          target: 'ready',
          actions: [
            sendTo('session', { type: 'STOP' }),
            assign(() => ({
              remoteStream: null,
              peerConnection: null,
              dataChannel: null,
              sessionId: null,
              diagnostics: defaultDiagnostics(),
              sessionPhase: 'idle',
            })),
          ],
        },
      },
    },
    serverError: {
      on: {
        STOP_SESSION: {
          target: 'ready',
          actions: [
            sendTo('session', { type: 'STOP' }),
            assign(() => ({
              remoteStream: null,
              peerConnection: null,
              dataChannel: null,
              sessionId: null,
              diagnostics: defaultDiagnostics(),
              sessionPhase: 'idle',
            })),
          ],
        },
      },
    },
  },
})
