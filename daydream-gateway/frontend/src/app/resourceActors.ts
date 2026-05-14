import { fromPromise } from 'xstate'
import type {
  ModelStatusResponse,
  PipelineSchemasResponse,
  PipelineStatusResponse,
  StreamMode,
} from '../types'
import {
  fetchModelStatus,
  fetchSchemas,
  fetchStatusSnapshot,
  loadPipeline,
  startModelDownload,
  waitForPipelineLoaded,
} from './scope'
import type { ModelStatusUi, PipelineStatusUi } from './studioTypes'
import { buildModelStatusUi, buildPipelineStatusUi } from './studioState'

export interface BootstrapData {
  schemas: PipelineSchemasResponse
  snapshot: {
    pipelineStatus: PipelineStatusResponse
    modelStatus: ModelStatusResponse
    pipelineStatusUi: PipelineStatusUi
    modelStatusUi: ModelStatusUi
  }
}

export interface RefreshData {
  pipelineStatus: PipelineStatusResponse
  modelStatus: ModelStatusResponse
  pipelineStatusUi: PipelineStatusUi
  modelStatusUi: ModelStatusUi
}

export interface ModelDownloadData {
  status: number
  body: {
    message?: string
  }
}

export interface PipelineLoadedData {
  pipelineStatus: PipelineStatusResponse
  modelStatus: ModelStatusResponse
  pipelineStatusUi: PipelineStatusUi
  modelStatusUi: ModelStatusUi
}

export interface SessionPreparedData {
  pipelineStatus: PipelineStatusResponse
  modelStatus: null
  pipelineStatusUi: PipelineStatusUi
}

export const bootstrapActor = fromPromise(async ({ input }: { input: { baseUrl: string; selectedPipelineId: string; mode: StreamMode } }) => {
  const [schemas, snapshot] = await Promise.all([
    fetchSchemas(input.baseUrl),
    fetchStatusSnapshot(input.baseUrl, input.selectedPipelineId),
  ])

  return {
    schemas: schemas.body,
    snapshot: {
      pipelineStatus: snapshot.pipelineStatus,
      modelStatus: snapshot.modelStatus,
      pipelineStatusUi: buildPipelineStatusUi(snapshot.pipelineStatus, input.selectedPipelineId),
      modelStatusUi: buildModelStatusUi(snapshot.modelStatus, input.selectedPipelineId),
    },
  } satisfies BootstrapData
})

export const refreshActor = fromPromise(async ({ input }: { input: { baseUrl: string; selectedPipelineId: string } }) => {
  const snapshot = await fetchStatusSnapshot(input.baseUrl, input.selectedPipelineId)
  return {
    ...snapshot,
    pipelineStatusUi: buildPipelineStatusUi(snapshot.pipelineStatus, input.selectedPipelineId),
    modelStatusUi: buildModelStatusUi(snapshot.modelStatus, input.selectedPipelineId),
  } satisfies RefreshData
})

export const requestCameraActor = fromPromise(async () => {
  return navigator.mediaDevices.getUserMedia({
    video: true,
    audio: false,
  })
})

export const downloadModelsActor = fromPromise(async ({ input }: { input: { baseUrl: string; selectedPipelineId: string } }) => {
  return (await startModelDownload(input.baseUrl, input.selectedPipelineId)) satisfies ModelDownloadData
})

export const loadPipelineActor = fromPromise(
  async ({ input }: { input: { baseUrl: string; selectedPipelineId: string; loadValues: Record<string, unknown> } }) => {
    await loadPipeline(input.baseUrl, input.selectedPipelineId, input.loadValues)
    const loadedStatus = await waitForPipelineLoaded(input.baseUrl, input.selectedPipelineId)
    const modelStatus = await fetchModelStatus(input.baseUrl, input.selectedPipelineId)

    return {
      pipelineStatus: loadedStatus,
      modelStatus: modelStatus.body,
      pipelineStatusUi: buildPipelineStatusUi(loadedStatus, input.selectedPipelineId),
      modelStatusUi: buildModelStatusUi(modelStatus.body, input.selectedPipelineId),
    } satisfies PipelineLoadedData
  },
)

export const ensureSessionPreparedActor = fromPromise(
  async ({
    input,
  }: {
    input: {
      baseUrl: string
      selectedPipelineId: string
      loadValues: Record<string, unknown>
      pipelineStatus: PipelineStatusUi
    }
  }) => {
    if (input.pipelineStatus.loadedPipelineId === input.selectedPipelineId && input.pipelineStatus.badge === 'loaded') {
      return {
        pipelineStatus: {
          status: 'loaded',
          pipeline_id: input.selectedPipelineId,
        } satisfies PipelineStatusResponse,
        modelStatus: null,
        pipelineStatusUi: {
          badge: 'loaded',
          stage: `Loaded: ${input.selectedPipelineId}`,
          loadedPipelineId: input.selectedPipelineId,
          loading: false,
        },
      } satisfies SessionPreparedData
    }

    await loadPipeline(input.baseUrl, input.selectedPipelineId, input.loadValues)
    const loadedStatus = await waitForPipelineLoaded(input.baseUrl, input.selectedPipelineId)
    return {
      pipelineStatus: loadedStatus,
      modelStatus: null,
      pipelineStatusUi: buildPipelineStatusUi(loadedStatus, input.selectedPipelineId),
    } satisfies SessionPreparedData
  },
)
