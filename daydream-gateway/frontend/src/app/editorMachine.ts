import { assign, sendParent, setup } from 'xstate'
import type { PromptInterpolation, StreamMode, PipelineSchema } from '../types'
import { applyLook, syncRuntimeFromMacros } from './studioState'
import type { MacroState, RuntimeValues, StudioContext } from './studioTypes'

export interface EditorContext {
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
}

export type EditorCommandEvent =
  | ({ type: 'EDITOR_SYNC' } & EditorContext)
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

export type EditorParentEvent = {
  type: 'EDITOR_PATCH'
  patch: Partial<
    Pick<
      EditorContext,
      | 'promptBase'
      | 'promptInterpolation'
      | 'transitionSteps'
      | 'selectedLookId'
      | 'macros'
      | 'loadValues'
      | 'runtimeValues'
      | 'loadControlMode'
      | 'runtimeControlMode'
    >
  >
}

const initialContext: EditorContext = {
  pipelineSchemas: {},
  selectedPipelineId: 'longlive',
  mode: 'receive',
  promptBase: '',
  promptInterpolation: 'linear',
  transitionSteps: 8,
  selectedLookId: 'cinematic',
  macros: {
    quality: 58,
    reference: 48,
    stability: 62,
  },
  loadValues: {},
  runtimeValues: {
    denoising_step_list: [1000, 750, 500, 250],
    manage_cache: true,
    reset_cache: false,
    noise_controller: true,
    noise_scale: 0.7,
    vace_context_scale: 1,
  },
  loadControlMode: 'basic',
  runtimeControlMode: 'basic',
}

function buildPatchedContext(context: EditorContext, patch: Partial<EditorContext>): EditorContext {
  return {
    ...context,
    ...patch,
  }
}

function asStudioContext(context: EditorContext): StudioContext {
  return context as unknown as StudioContext
}

export const editorMachine = setup({
  types: {
    context: {} as EditorContext,
    events: {} as EditorCommandEvent,
  },
}).createMachine({
  id: 'editor',
  initial: 'active',
  context: initialContext,
  states: {
    active: {
      on: {
        EDITOR_SYNC: {
          actions: assign(({ event }) => ({
            pipelineSchemas: event.pipelineSchemas,
            selectedPipelineId: event.selectedPipelineId,
            mode: event.mode,
            promptBase: event.promptBase,
            promptInterpolation: event.promptInterpolation,
            transitionSteps: event.transitionSteps,
            selectedLookId: event.selectedLookId,
            macros: event.macros,
            loadValues: event.loadValues,
            runtimeValues: event.runtimeValues,
            loadControlMode: event.loadControlMode,
            runtimeControlMode: event.runtimeControlMode,
          })),
        },
        SET_PROMPT: {
          actions: [
            assign({
              promptBase: ({ event }) => event.value,
            }),
            sendParent(({ event }) => ({
              type: 'EDITOR_PATCH',
              patch: { promptBase: event.value },
            })),
          ],
        },
        SET_PROMPT_INTERPOLATION: {
          actions: [
            assign({
              promptInterpolation: ({ event }) => event.value,
            }),
            sendParent(({ event }) => ({
              type: 'EDITOR_PATCH',
              patch: { promptInterpolation: event.value },
            })),
          ],
        },
        SET_TRANSITION_STEPS: {
          actions: [
            assign({
              transitionSteps: ({ event }) => event.value,
            }),
            sendParent(({ event }) => ({
              type: 'EDITOR_PATCH',
              patch: { transitionSteps: event.value },
            })),
          ],
        },
        SELECT_LOOK: {
          actions: [
            assign(({ context, event }) => {
              const patch = applyLook(asStudioContext(context), event.lookId)
              return buildPatchedContext(context, patch)
            }),
            sendParent(({ context, event }) => ({
              type: 'EDITOR_PATCH',
              patch: applyLook(asStudioContext(context), event.lookId),
            })),
          ],
        },
        SET_MACRO: {
          actions: [
            assign(({ context, event }) => {
              const macros = {
                ...context.macros,
                [event.key]: event.value,
              }
              return {
                macros,
                runtimeValues: syncRuntimeFromMacros(context.runtimeValues, macros),
              }
            }),
            sendParent(({ context, event }) => {
              const macros = {
                ...context.macros,
                [event.key]: event.value,
              }
              return {
                type: 'EDITOR_PATCH',
                patch: {
                  macros,
                  runtimeValues: syncRuntimeFromMacros(context.runtimeValues, macros),
                },
              }
            }),
          ],
        },
        SET_LOAD_CONTROL_MODE: {
          actions: [
            assign({
              loadControlMode: ({ event }) => event.value,
            }),
            sendParent(({ event }) => ({
              type: 'EDITOR_PATCH',
              patch: { loadControlMode: event.value },
            })),
          ],
        },
        SET_RUNTIME_CONTROL_MODE: {
          actions: [
            assign({
              runtimeControlMode: ({ event }) => event.value,
            }),
            sendParent(({ event }) => ({
              type: 'EDITOR_PATCH',
              patch: { runtimeControlMode: event.value },
            })),
          ],
        },
        SET_LOAD_VALUE: {
          actions: [
            assign(({ context, event }) => {
              const loadValues = {
                ...context.loadValues,
                [event.key]: event.value,
              }
              const next = { ...context, loadValues }
              if (event.key === 'vace_enabled') {
                return buildPatchedContext(context, {
                  loadValues,
                  ...applyLook(asStudioContext(next as EditorContext)),
                })
              }
              return {
                loadValues,
              }
            }),
            sendParent(({ context, event }) => {
              const loadValues = {
                ...context.loadValues,
                [event.key]: event.value,
              }
              const next = { ...context, loadValues }
              return {
                type: 'EDITOR_PATCH',
                patch:
                  event.key === 'vace_enabled'
                    ? {
                        loadValues,
                        ...applyLook(asStudioContext(next as EditorContext)),
                      }
                    : { loadValues },
              }
            }),
          ],
        },
        SET_RUNTIME_VALUE: {
          actions: [
            assign({
              runtimeValues: ({ context, event }) => ({
                ...context.runtimeValues,
                [event.key]: event.value,
              }),
            }),
            sendParent(({ context, event }) => ({
              type: 'EDITOR_PATCH',
              patch: {
                runtimeValues: {
                  ...context.runtimeValues,
                  [event.key]: event.value,
                },
              },
            })),
          ],
        },
        APPLY_LOAD_PRESET: {
          actions: [
            assign({
              loadValues: ({ context, event }) => ({
                ...context.loadValues,
                width: event.width,
                height: event.height,
              }),
            }),
            sendParent(({ context, event }) => ({
              type: 'EDITOR_PATCH',
              patch: {
                loadValues: {
                  ...context.loadValues,
                  width: event.width,
                  height: event.height,
                },
              },
            })),
          ],
        },
        APPLY_RUNTIME_PRESET: {
          actions: [
            assign({
              runtimeValues: ({ context, event }) => ({
                ...context.runtimeValues,
                denoising_step_list: [...event.values],
              }),
            }),
            sendParent(({ context, event }) => ({
              type: 'EDITOR_PATCH',
              patch: {
                runtimeValues: {
                  ...context.runtimeValues,
                  denoising_step_list: [...event.values],
                },
              },
            })),
          ],
        },
      },
    },
  },
})
