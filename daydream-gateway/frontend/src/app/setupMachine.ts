import { assign, sendParent, setup } from 'xstate'
import type { PipelineSchemasResponse, PipelineSchema, StreamMode } from '../types'
import { defaultBaseUrl, fallbackPipelineId } from './studioTypes'

export interface SetupContext {
  baseUrl: string
  availablePipelines: string[]
  pipelineSchemas: Record<string, PipelineSchema>
  selectedPipelineId: string
  mode: StreamMode
}

export type SetupCommandEvent =
  | {
      type: 'SETUP_BOOTSTRAP'
      baseUrl: string
      schemas: PipelineSchemasResponse
      selectedPipelineId: string
      mode: StreamMode
    }
  | { type: 'SET_BASE_URL'; value: string }
  | { type: 'SELECT_PIPELINE'; pipelineId: string }
  | { type: 'SET_MODE'; mode: StreamMode }

export type SetupParentEvent = {
  type: 'SETUP_PATCH'
  patch: Partial<Pick<SetupContext, 'baseUrl' | 'availablePipelines' | 'pipelineSchemas' | 'selectedPipelineId' | 'mode'>>
}

const initialContext: SetupContext = {
  baseUrl: defaultBaseUrl,
  availablePipelines: [fallbackPipelineId],
  pipelineSchemas: {},
  selectedPipelineId: fallbackPipelineId,
  mode: 'receive',
}

export const setupMachine = setup({
  types: {
    context: {} as SetupContext,
    events: {} as SetupCommandEvent,
  },
}).createMachine({
  id: 'setup',
  initial: 'active',
  context: initialContext,
  states: {
    active: {
      on: {
        SETUP_BOOTSTRAP: {
          actions: [
            assign(({ event }) => {
              const pipelineSchemas = event.schemas.pipelines || {}
              const availablePipelines = Object.keys(pipelineSchemas)
              const selectedPipelineId = availablePipelines.includes(event.selectedPipelineId)
                ? event.selectedPipelineId
                : availablePipelines[0] || fallbackPipelineId

              return {
                baseUrl: event.baseUrl,
                pipelineSchemas,
                availablePipelines: availablePipelines.length ? availablePipelines : [fallbackPipelineId],
                selectedPipelineId,
                mode: event.mode,
              }
            }),
            sendParent(({ event }) => {
              const pipelineSchemas = event.schemas.pipelines || {}
              const availablePipelines = Object.keys(pipelineSchemas)
              const selectedPipelineId = availablePipelines.includes(event.selectedPipelineId)
                ? event.selectedPipelineId
                : availablePipelines[0] || fallbackPipelineId

              return {
                type: 'SETUP_PATCH',
                patch: {
                  baseUrl: event.baseUrl,
                  pipelineSchemas,
                  availablePipelines: availablePipelines.length ? availablePipelines : [fallbackPipelineId],
                  selectedPipelineId,
                  mode: event.mode,
                },
              }
            }),
          ],
        },
        SET_BASE_URL: {
          actions: [
            assign({
              baseUrl: ({ event }) => event.value,
            }),
            sendParent(({ event }) => ({
              type: 'SETUP_PATCH',
              patch: { baseUrl: event.value },
            })),
          ],
        },
        SELECT_PIPELINE: {
          actions: [
            assign({
              selectedPipelineId: ({ event }) => event.pipelineId,
            }),
            sendParent(({ event }) => ({
              type: 'SETUP_PATCH',
              patch: { selectedPipelineId: event.pipelineId },
            })),
          ],
        },
        SET_MODE: {
          actions: [
            assign({
              mode: ({ event }) => event.mode,
            }),
            sendParent(({ event }) => ({
              type: 'SETUP_PATCH',
              patch: { mode: event.mode },
            })),
          ],
        },
      },
    },
  },
})
