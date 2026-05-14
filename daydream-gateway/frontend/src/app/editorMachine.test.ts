import { createActor, assign, sendTo, setup } from 'xstate'
import { describe, expect, it } from 'vitest'
import { editorMachine, type EditorCommandEvent, type EditorParentEvent } from './editorMachine'

type HarnessEvent = EditorCommandEvent | EditorParentEvent

const editorHarnessMachine = setup({
  types: {
    context: {} as {
      selectedLookId: string
      promptInterpolation: 'linear' | 'slerp'
      transitionSteps: number
      macros: {
        quality: number
        reference: number
        stability: number
      }
      runtimeValues: {
        denoising_step_list: number[]
        manage_cache: boolean
        reset_cache: boolean
        noise_controller?: boolean
        noise_scale?: number
        vace_context_scale?: number
      }
      loadValues: Record<string, unknown>
    },
    events: {} as HarnessEvent,
  },
  actors: {
    child: editorMachine,
  },
}).createMachine({
  context: {
    selectedLookId: 'cinematic',
    promptInterpolation: 'linear',
    transitionSteps: 8,
    macros: {
      quality: 58,
      reference: 48,
      stability: 62,
    },
    runtimeValues: {
      denoising_step_list: [1000, 750, 500, 250],
      manage_cache: true,
      reset_cache: false,
      noise_controller: true,
      noise_scale: 0.7,
      vace_context_scale: 1,
    },
    loadValues: {
      vace_enabled: true,
    },
  },
  invoke: {
    id: 'child',
    src: 'child',
  },
  on: {
    EDITOR_PATCH: {
      actions: assign(({ context, event }) => ({
        ...context,
        ...event.patch,
      })),
    },
    EDITOR_SYNC: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_PROMPT: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_PROMPT_INTERPOLATION: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_TRANSITION_STEPS: {
      actions: sendTo('child', ({ event }) => event),
    },
    SELECT_LOOK: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_MACRO: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_LOAD_CONTROL_MODE: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_RUNTIME_CONTROL_MODE: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_LOAD_VALUE: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_RUNTIME_VALUE: {
      actions: sendTo('child', ({ event }) => event),
    },
    APPLY_LOAD_PRESET: {
      actions: sendTo('child', ({ event }) => event),
    },
    APPLY_RUNTIME_PRESET: {
      actions: sendTo('child', ({ event }) => event),
    },
  },
})

function createEditorActor() {
  const actor = createActor(editorHarnessMachine)
  actor.start()
  actor.send({
    type: 'EDITOR_SYNC',
    pipelineSchemas: {
      longlive: {
        supports_vace: true,
        config_schema: {
          properties: {},
        },
      },
    },
    selectedPipelineId: 'longlive',
    mode: 'receive',
    promptBase: 'A neon city at night',
    promptInterpolation: 'linear',
    transitionSteps: 8,
    selectedLookId: 'cinematic',
    macros: {
      quality: 58,
      reference: 48,
      stability: 62,
    },
    loadValues: {
      vace_enabled: true,
    },
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
  })
  return actor
}

describe('editorMachine', () => {
  it('applies an adapted look and updates creative controls', () => {
    const actor = createEditorActor()

    actor.send({ type: 'SELECT_LOOK', lookId: 'dreamlike' })

    const snapshot = actor.getSnapshot()
    expect(snapshot.context.selectedLookId).toBe('dreamlike')
    expect(snapshot.context.promptInterpolation).toBe('slerp')
    expect(snapshot.context.transitionSteps).toBe(12)
    expect(snapshot.context.macros).toEqual({
      quality: 84,
      reference: 70,
      stability: 40,
    })
    expect(snapshot.context.runtimeValues.denoising_step_list).toEqual([1000, 850, 700, 550, 400, 250])
    expect(snapshot.context.runtimeValues.vace_context_scale).toBe(1.35)
    expect(snapshot.context.runtimeValues.noise_scale).toBe(0.66)
    expect(snapshot.context.runtimeValues.noise_controller).toBe(false)
  })

  it('maps macro changes back into runtime values', () => {
    const actor = createEditorActor()

    actor.send({ type: 'SET_MACRO', key: 'stability', value: 90 })

    const snapshot = actor.getSnapshot()
    expect(snapshot.context.macros.stability).toBe(90)
    expect(snapshot.context.runtimeValues.noise_scale).toBe(0.36)
    expect(snapshot.context.runtimeValues.noise_controller).toBe(true)
  })

  it('turns off VACE and re-adapts the current look', () => {
    const actor = createEditorActor()
    actor.send({ type: 'SELECT_LOOK', lookId: 'dreamlike' })
    actor.send({ type: 'SET_LOAD_VALUE', key: 'vace_enabled', value: false })

    const snapshot = actor.getSnapshot()
    expect(snapshot.context.loadValues.vace_enabled).toBe(false)
    expect(snapshot.context.macros.reference).toBe(42)
    expect(snapshot.context.runtimeValues.vace_context_scale).toBe(0.9)
  })
})
