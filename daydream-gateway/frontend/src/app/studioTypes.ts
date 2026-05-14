import type {
  PipelineSchema,
  PromptInterpolation,
  StreamMode,
} from '../types'

export const defaultBaseUrl = ''
export const defaultPrompt =
  'A neon city at night with cinematic lighting and drifting fog'
export const fallbackPipelineId = 'longlive'

export interface BannerState {
  message: string
  tone: 'info' | 'warning' | 'error'
}

export interface DiagnosticsState {
  connection: string
  ice: string
  signaling: string
  track: string
  video: string
  frames: string
  bytes: string
  decoded: string
  session: string
}

export interface PipelineStatusUi {
  badge: string
  stage: string
  loadedPipelineId: string | null
  loading: boolean
}

export interface ModelStatusUi {
  badge: string
  stage: string
  downloaded: boolean
  downloading: boolean
}

export interface MacroState {
  quality: number
  reference: number
  stability: number
}

export interface RuntimeValues {
  denoising_step_list: number[]
  noise_scale?: number
  noise_controller?: boolean
  manage_cache: boolean
  reset_cache: boolean
  vace_context_scale?: number
}

export type ResourceActivity =
  | 'idle'
  | 'bootstrapping'
  | 'refreshing'
  | 'downloadingModels'
  | 'loadingPipeline'
  | 'preparingSession'
  | 'requestingCamera'

export type SessionPhase =
  | 'idle'
  | 'fetchingIce'
  | 'creatingOffer'
  | 'awaitingMedia'
  | 'connected'
  | 'streaming'
  | 'failed'
  | 'stopped'
  | 'serverError'

export interface StudioContext {
  baseUrl: string
  availablePipelines: string[]
  pipelineSchemas: Record<string, PipelineSchema>
  selectedPipelineId: string
  mode: StreamMode
  promptBase: string
  promptInterpolation: PromptInterpolation
  transitionSteps: number
  selectedLookId: string
  macros: MacroState
  loadValues: Record<string, unknown>
  runtimeValues: RuntimeValues
  loadControlMode: 'basic' | 'advanced'
  runtimeControlMode: 'basic' | 'advanced'
  pipelineStatus: PipelineStatusUi
  modelStatus: ModelStatusUi
  banner: BannerState | null
  logs: string[]
  diagnostics: DiagnosticsState
  sessionLabel: string
  sessionId: string | null
  peerConnection: RTCPeerConnection | null
  dataChannel: RTCDataChannel | null
  localStream: MediaStream | null
  remoteStream: MediaStream | null
  pendingSessionStart: boolean
  resourceActivity: ResourceActivity
  sessionPhase: SessionPhase
}
