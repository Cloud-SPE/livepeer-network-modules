import { sendParent, setup } from 'xstate'
import {
  bootstrapActor,
  downloadModelsActor,
  ensureSessionPreparedActor,
  loadPipelineActor,
  refreshActor,
  requestCameraActor,
  type BootstrapData,
  type ModelDownloadData,
  type PipelineLoadedData,
  type RefreshData,
  type SessionPreparedData,
} from './resourceActors'
import type { PipelineStatusUi, ResourceActivity } from './studioTypes'
import type { StreamMode } from '../types'

export type ResourcesCommandEvent =
  | { type: 'BOOTSTRAP'; baseUrl: string; selectedPipelineId: string; mode: StreamMode }
  | { type: 'REFRESH'; baseUrl: string; selectedPipelineId: string }
  | { type: 'DOWNLOAD_MODELS'; baseUrl: string; selectedPipelineId: string }
  | { type: 'LOAD_PIPELINE'; baseUrl: string; selectedPipelineId: string; loadValues: Record<string, unknown> }
  | {
      type: 'PREPARE_SESSION'
      baseUrl: string
      selectedPipelineId: string
      loadValues: Record<string, unknown>
      pipelineStatus: PipelineStatusUi
    }
  | { type: 'REQUEST_CAMERA' }

export type ResourcesParentEvent =
  | { type: 'RESOURCES_STATUS'; activity: ResourceActivity }
  | { type: 'RESOURCES_BOOTSTRAPPED'; output: BootstrapData }
  | { type: 'RESOURCES_REFRESHED'; output: RefreshData }
  | { type: 'RESOURCES_MODEL_DOWNLOAD_STARTED'; output: ModelDownloadData }
  | { type: 'RESOURCES_PIPELINE_LOADED'; output: PipelineLoadedData }
  | { type: 'RESOURCES_SESSION_PREPARED'; output: SessionPreparedData }
  | { type: 'RESOURCES_CAMERA_READY'; stream: MediaStream }
  | { type: 'RESOURCES_ERROR'; scope: 'bootstrap' | 'refresh' | 'download' | 'load' | 'prepare' | 'camera'; error: unknown }

export const resourcesMachine = setup({
  types: {
    events: {} as ResourcesCommandEvent,
  },
  actors: {
    bootstrapActor,
    refreshActor,
    downloadModelsActor,
    loadPipelineActor,
    ensureSessionPreparedActor,
    requestCameraActor,
  },
}).createMachine({
  id: 'resources',
  initial: 'idle',
  states: {
    idle: {
      entry: sendParent({
        type: 'RESOURCES_STATUS',
        activity: 'idle',
      }),
      on: {
        BOOTSTRAP: 'bootstrapping',
        REFRESH: 'refreshing',
        DOWNLOAD_MODELS: 'downloadingModels',
        LOAD_PIPELINE: 'loadingPipeline',
        PREPARE_SESSION: 'preparingSession',
        REQUEST_CAMERA: 'requestingCamera',
      },
    },
    bootstrapping: {
      entry: sendParent({
        type: 'RESOURCES_STATUS',
        activity: 'bootstrapping',
      }),
      invoke: {
        src: 'bootstrapActor',
        input: ({ event }) => {
          const command = event as Extract<ResourcesCommandEvent, { type: 'BOOTSTRAP' }>
          return {
            baseUrl: command.baseUrl,
            selectedPipelineId: command.selectedPipelineId,
            mode: command.mode,
          }
        },
        onDone: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_BOOTSTRAPPED',
            output: event.output,
          })),
        },
        onError: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_ERROR',
            scope: 'bootstrap',
            error: event.error,
          })),
        },
      },
    },
    refreshing: {
      entry: sendParent({
        type: 'RESOURCES_STATUS',
        activity: 'refreshing',
      }),
      invoke: {
        src: 'refreshActor',
        input: ({ event }) => {
          const command = event as Extract<ResourcesCommandEvent, { type: 'REFRESH' }>
          return {
            baseUrl: command.baseUrl,
            selectedPipelineId: command.selectedPipelineId,
          }
        },
        onDone: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_REFRESHED',
            output: event.output,
          })),
        },
        onError: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_ERROR',
            scope: 'refresh',
            error: event.error,
          })),
        },
      },
    },
    downloadingModels: {
      entry: sendParent({
        type: 'RESOURCES_STATUS',
        activity: 'downloadingModels',
      }),
      invoke: {
        src: 'downloadModelsActor',
        input: ({ event }) => {
          const command = event as Extract<ResourcesCommandEvent, { type: 'DOWNLOAD_MODELS' }>
          return {
            baseUrl: command.baseUrl,
            selectedPipelineId: command.selectedPipelineId,
          }
        },
        onDone: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_MODEL_DOWNLOAD_STARTED',
            output: event.output,
          })),
        },
        onError: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_ERROR',
            scope: 'download',
            error: event.error,
          })),
        },
      },
    },
    loadingPipeline: {
      entry: sendParent({
        type: 'RESOURCES_STATUS',
        activity: 'loadingPipeline',
      }),
      invoke: {
        src: 'loadPipelineActor',
        input: ({ event }) => {
          const command = event as Extract<ResourcesCommandEvent, { type: 'LOAD_PIPELINE' }>
          return {
            baseUrl: command.baseUrl,
            selectedPipelineId: command.selectedPipelineId,
            loadValues: command.loadValues,
          }
        },
        onDone: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_PIPELINE_LOADED',
            output: event.output,
          })),
        },
        onError: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_ERROR',
            scope: 'load',
            error: event.error,
          })),
        },
      },
    },
    preparingSession: {
      entry: sendParent({
        type: 'RESOURCES_STATUS',
        activity: 'preparingSession',
      }),
      invoke: {
        src: 'ensureSessionPreparedActor',
        input: ({ event }) => {
          const command = event as Extract<ResourcesCommandEvent, { type: 'PREPARE_SESSION' }>
          return {
            baseUrl: command.baseUrl,
            selectedPipelineId: command.selectedPipelineId,
            loadValues: command.loadValues,
            pipelineStatus: command.pipelineStatus,
          }
        },
        onDone: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_SESSION_PREPARED',
            output: event.output,
          })),
        },
        onError: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_ERROR',
            scope: 'prepare',
            error: event.error,
          })),
        },
      },
    },
    requestingCamera: {
      entry: sendParent({
        type: 'RESOURCES_STATUS',
        activity: 'requestingCamera',
      }),
      invoke: {
        src: 'requestCameraActor',
        onDone: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_CAMERA_READY',
            stream: event.output,
          })),
        },
        onError: {
          target: 'idle',
          actions: sendParent(({ event }) => ({
            type: 'RESOURCES_ERROR',
            scope: 'camera',
            error: event.error,
          })),
        },
      },
    },
  },
})
