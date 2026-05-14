import {
  buildPromptWithLook,
  getAdaptedLookPreset,
  getNoiseScaleForStability,
  getQualityPresetForValue,
  getReferenceScaleForValue,
} from '../looks'
import type {
  InitialParameters,
  LookContext,
  ModelStatusResponse,
  PipelineSchema,
  PipelineSchemasResponse,
  PipelineStatusResponse,
  StreamMode,
} from '../types'
import { stringifyPayload } from './scope'
import type {
  DiagnosticsState,
  MacroState,
  ModelStatusUi,
  PipelineStatusUi,
  RuntimeValues,
  StudioContext,
} from './studioTypes'
import { defaultPrompt, fallbackPipelineId } from './studioTypes'

export const defaultDiagnostics = (): DiagnosticsState => ({
  connection: 'idle',
  ice: 'idle',
  signaling: 'idle',
  track: 'none',
  video: '0 x 0',
  frames: 'waiting',
  bytes: '0',
  decoded: '0',
  session: 'none',
})

export const defaultPipelineStatus = (): PipelineStatusUi => ({
  badge: 'unknown',
  stage: 'Pipeline status not checked yet.',
  loadedPipelineId: null,
  loading: false,
})

export const defaultModelStatus = (): ModelStatusUi => ({
  badge: 'unknown',
  stage: 'Model status not checked yet.',
  downloaded: false,
  downloading: false,
})

export const defaultMacros: MacroState = {
  quality: 58,
  reference: 48,
  stability: 62,
}

export const defaultRuntimeValues: RuntimeValues = {
  denoising_step_list: [1000, 750, 500, 250],
  manage_cache: true,
  reset_cache: false,
  noise_controller: true,
  noise_scale: 0.7,
  vace_context_scale: 1,
}

export function appendLog(logs: string[], label: string, payload: unknown) {
  return [`[${new Date().toLocaleTimeString()}] ${label}\n${stringifyPayload(payload)}`, ...logs].slice(0, 200)
}

export function getSelectedSchema(context: StudioContext) {
  return context.pipelineSchemas[context.selectedPipelineId] || null
}

export function getPipelinePropertyDefault(
  schema: PipelineSchema | null,
  propertyName: string,
  mode: StreamMode,
) {
  const property = schema?.config_schema?.properties?.[propertyName]
  if (!property) {
    return undefined
  }

  const modeDefaults = schema?.mode_defaults?.[mode]
  if (modeDefaults && modeDefaults[propertyName] !== undefined) {
    return modeDefaults[propertyName]
  }

  return property.default
}

export function buildDefaultLoadValues(schema: PipelineSchema | null, mode: StreamMode) {
  if (!schema?.config_schema?.properties) {
    return {}
  }

  const propertyNames = ['height', 'width', 'base_seed', 'vae_type', 'quantization', 'manage_cache']
  const values: Record<string, unknown> = {}

  for (const propertyName of propertyNames) {
    const value = getPipelinePropertyDefault(schema, propertyName, mode)
    if (value !== undefined) {
      values[propertyName] = value
    }
  }

  if (schema.supports_vace) {
    values.vace_enabled = true
  }

  return values
}

export function buildDefaultRuntimeValues(schema: PipelineSchema | null, mode: StreamMode): RuntimeValues {
  return {
    denoising_step_list:
      (getPipelinePropertyDefault(schema, 'denoising_steps', mode) as number[] | undefined) ||
      defaultRuntimeValues.denoising_step_list,
    noise_scale:
      (getPipelinePropertyDefault(schema, 'noise_scale', mode) as number | undefined) ??
      defaultRuntimeValues.noise_scale,
    noise_controller:
      (getPipelinePropertyDefault(schema, 'noise_controller', mode) as boolean | undefined) ??
      defaultRuntimeValues.noise_controller,
    manage_cache:
      (getPipelinePropertyDefault(schema, 'manage_cache', mode) as boolean | undefined) ??
      defaultRuntimeValues.manage_cache,
    reset_cache: false,
    vace_context_scale:
      (getPipelinePropertyDefault(schema, 'vace_context_scale', mode) as number | undefined) ??
      defaultRuntimeValues.vace_context_scale,
  }
}

export function isVaceEnabled(context: StudioContext) {
  const schema = getSelectedSchema(context)
  if (!schema?.supports_vace) {
    return false
  }

  const explicit = context.loadValues.vace_enabled
  return explicit === undefined ? true : Boolean(explicit)
}

export function getLookContext(context: StudioContext): LookContext {
  return {
    mode: context.mode,
    pipelineId: context.selectedPipelineId,
    vaceEnabled: isVaceEnabled(context),
  }
}

export function syncRuntimeFromMacros(runtimeValues: RuntimeValues, macros: MacroState): RuntimeValues {
  const qualityPreset = getQualityPresetForValue(macros.quality)
  return {
    ...runtimeValues,
    denoising_step_list: [...qualityPreset.value],
    vace_context_scale: getReferenceScaleForValue(macros.reference),
    noise_scale: getNoiseScaleForStability(macros.stability),
    noise_controller: macros.stability >= 45,
  }
}

export function getAdaptedLook(context: StudioContext) {
  return getAdaptedLookPreset(context.selectedLookId, getLookContext(context))
}

export function getPromptPayload(context: StudioContext) {
  return [
    {
      text: buildPromptWithLook(context.promptBase.trim() || defaultPrompt, getAdaptedLook(context)),
      weight: 1,
    },
  ]
}

export function getInitialParameters(context: StudioContext): InitialParameters {
  return {
    pipeline_ids: [context.selectedPipelineId],
    input_mode: context.mode === 'webcam' ? 'video' : undefined,
    prompts: getPromptPayload(context),
    prompt_interpolation_method: context.promptInterpolation,
    denoising_step_list: context.runtimeValues.denoising_step_list,
    noise_scale: context.runtimeValues.noise_scale,
    noise_controller: context.runtimeValues.noise_controller,
    manage_cache: context.runtimeValues.manage_cache,
    reset_cache: context.runtimeValues.reset_cache,
    vace_context_scale: context.runtimeValues.vace_context_scale,
  }
}

export function applyLook(context: StudioContext, nextLookId = context.selectedLookId) {
  const withLook = { ...context, selectedLookId: nextLookId }
  const adapted = getAdaptedLookPreset(nextLookId, getLookContext(withLook))
  if (!adapted) {
    return {
      selectedLookId: nextLookId,
    }
  }

  return {
    selectedLookId: nextLookId,
    macros: adapted.macros,
    promptInterpolation: adapted.transition.interpolation,
    transitionSteps: adapted.transition.steps,
    runtimeValues: syncRuntimeFromMacros(withLook.runtimeValues, adapted.macros),
  }
}

export function buildPipelineStatusUi(status: PipelineStatusResponse, selectedPipelineId: string): PipelineStatusUi {
  if (status.status === 'loaded' && status.pipeline_id && status.pipeline_id !== selectedPipelineId) {
    return {
      badge: 'other loaded',
      stage: `Loaded pipeline is ${status.pipeline_id}. Selected pipeline is ${selectedPipelineId}.`,
      loadedPipelineId: status.pipeline_id,
      loading: false,
    }
  }

  if (status.status === 'loaded') {
    return {
      badge: 'loaded',
      stage: `Loaded: ${status.pipeline_id || 'unknown pipeline'}`,
      loadedPipelineId: status.pipeline_id || null,
      loading: false,
    }
  }

  if (status.status === 'loading') {
    return {
      badge: 'loading',
      stage: status.loading_stage || 'Pipeline is loading...',
      loadedPipelineId: status.pipeline_id || null,
      loading: true,
    }
  }

  if (status.status === 'error') {
    return {
      badge: 'error',
      stage: status.error || 'Pipeline load failed.',
      loadedPipelineId: status.pipeline_id || null,
      loading: false,
    }
  }

  if (status.status === 'not_loaded') {
    return {
      badge: 'not loaded',
      stage: 'No pipeline is currently loaded.',
      loadedPipelineId: null,
      loading: false,
    }
  }

  return {
    badge: status.status || 'unknown',
    stage: 'Pipeline state is unknown.',
    loadedPipelineId: status.pipeline_id || null,
    loading: false,
  }
}

export function buildModelStatusUi(modelStatus: ModelStatusResponse, pipelineId: string): ModelStatusUi {
  if (modelStatus?.downloaded) {
    return {
      badge: 'downloaded',
      stage: `${pipelineId} model files are ready.`,
      downloaded: true,
      downloading: false,
    }
  }

  if (modelStatus?.progress?.is_downloading) {
    return {
      badge: 'downloading',
      stage: modelStatus.progress.message || `Downloading ${pipelineId} models...`,
      downloaded: false,
      downloading: true,
    }
  }

  if (modelStatus?.progress?.error) {
    return {
      badge: 'error',
      stage: modelStatus.progress.error,
      downloaded: false,
      downloading: false,
    }
  }

  return {
    badge: 'missing',
    stage: `${pipelineId} model files are not downloaded.`,
    downloaded: false,
    downloading: false,
  }
}

export function buildSchemasState(
  schemas: PipelineSchemasResponse,
  selectedPipelineId: string,
  mode: StreamMode,
) {
  const pipelineSchemas = schemas.pipelines || {}
  const availablePipelines = Object.keys(pipelineSchemas)
  const nextPipelineId = availablePipelines.includes(selectedPipelineId)
    ? selectedPipelineId
    : availablePipelines[0] || fallbackPipelineId
  const schema = pipelineSchemas[nextPipelineId] || null

  return {
    availablePipelines: availablePipelines.length ? availablePipelines : [fallbackPipelineId],
    pipelineSchemas,
    selectedPipelineId: nextPipelineId,
    loadValues: buildDefaultLoadValues(schema, mode),
    runtimeValues: buildDefaultRuntimeValues(schema, mode),
  }
}
