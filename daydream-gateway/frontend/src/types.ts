export type StreamMode = 'receive' | 'webcam'
export type PromptInterpolation = 'linear' | 'slerp'

export interface PromptItem {
  text: string
  weight: number
}

export interface PromptTransition {
  target_prompts: PromptItem[]
  num_steps: number
  temporal_interpolation_method?: PromptInterpolation
}

export interface InitialParameters {
  pipeline_ids: string[]
  input_mode?: 'video'
  prompts?: PromptItem[]
  prompt_interpolation_method?: PromptInterpolation
  transition?: PromptTransition
  denoising_step_list?: number[]
  noise_scale?: number
  noise_controller?: boolean
  manage_cache?: boolean
  reset_cache?: boolean
  vace_context_scale?: number
}

export interface PipelineStatusResponse {
  status?: string
  pipeline_id?: string | null
  error?: string | null
  loading_stage?: string | null
}

export interface ModelDownloadProgress {
  is_downloading?: boolean
  message?: string
  error?: string
}

export interface ModelStatusResponse {
  downloaded?: boolean
  progress?: ModelDownloadProgress | null
}

export interface IceServerConfig {
  urls: string | string[]
  username?: string | null
  credential?: string | null
}

export interface IceServersResponse {
  iceServers?: IceServerConfig[]
}

export interface WebRTCOfferResponse {
  sdp: string
  type: RTCSdpType
  sessionId: string
}

export interface PipelineUiProperty {
  type?: string
  title?: string
  default?: unknown
  description?: string
  minimum?: number
  maximum?: number
  enum?: Array<string | number>
  anyOf?: Array<{ type?: string; enum?: Array<string | number> }>
  ui?: {
    label?: string
  }
}

export interface PipelineSchema {
  supported_modes?: string[]
  inputs?: string[]
  outputs?: string[]
  estimated_vram_gb?: number | null
  supports_vace?: boolean
  supports_lora?: boolean
  supports_quantization?: boolean
  mode_defaults?: Record<string, Record<string, unknown>>
  config_schema?: {
    properties?: Record<string, PipelineUiProperty>
  }
}

export interface PipelineSchemasResponse {
  pipelines?: Record<string, PipelineSchema>
}

export interface LookPresetDefinition {
  id: string
  label: string
  note: string
  suffix: string
  macros: {
    quality: number
    reference: number
    stability: number
  }
  transition: {
    steps: number
    interpolation: PromptInterpolation
  }
  recommendedPipelines: Partial<Record<StreamMode | 'receive', string>>
}

export interface AdaptedLookPreset extends LookPresetDefinition {
  recommendedPipeline: string
}

export interface LookContext {
  mode: StreamMode
  pipelineId: string
  vaceEnabled: boolean
}
