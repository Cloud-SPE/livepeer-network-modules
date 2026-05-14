import { createActor, assign, sendTo, setup } from 'xstate'
import { describe, expect, it } from 'vitest'
import { setupMachine, type SetupCommandEvent, type SetupParentEvent } from './setupMachine'

type HarnessEvent = SetupCommandEvent | SetupParentEvent

const setupHarnessMachine = setup({
  types: {
    context: {} as {
      baseUrl: string
      availablePipelines: string[]
      selectedPipelineId: string
      mode: 'receive' | 'webcam'
    },
    events: {} as HarnessEvent,
  },
  actors: {
    child: setupMachine,
  },
}).createMachine({
  context: {
    baseUrl: '',
    availablePipelines: [],
    selectedPipelineId: '',
    mode: 'receive',
  },
  invoke: {
    id: 'child',
    src: 'child',
  },
  on: {
    SETUP_PATCH: {
      actions: assign(({ context, event }) => ({
        ...context,
        ...event.patch,
      })),
    },
    SETUP_BOOTSTRAP: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_BASE_URL: {
      actions: sendTo('child', ({ event }) => event),
    },
    SELECT_PIPELINE: {
      actions: sendTo('child', ({ event }) => event),
    },
    SET_MODE: {
      actions: sendTo('child', ({ event }) => event),
    },
  },
})

describe('setupMachine', () => {
  it('bootstraps pipeline catalog and falls back to the first available pipeline', () => {
    const actor = createActor(setupHarnessMachine)
    actor.start()

    actor.send({
      type: 'SETUP_BOOTSTRAP',
      baseUrl: 'https://example.test',
      selectedPipelineId: 'missing-pipeline',
      mode: 'webcam',
      schemas: {
        pipelines: {
          streamdiffusionv2: { supported_modes: ['text', 'video'] },
          longlive: { supported_modes: ['text', 'video'] },
        },
      },
    })

    const snapshot = actor.getSnapshot()
    expect(snapshot.context.baseUrl).toBe('https://example.test')
    expect(snapshot.context.availablePipelines).toEqual(['streamdiffusionv2', 'longlive'])
    expect(snapshot.context.selectedPipelineId).toBe('streamdiffusionv2')
    expect(snapshot.context.mode).toBe('webcam')
  })

  it('updates base URL, selected pipeline, and mode through setup events', () => {
    const actor = createActor(setupHarnessMachine)
    actor.start()

    actor.send({ type: 'SET_BASE_URL', value: 'https://scope.xode.live' })
    actor.send({ type: 'SELECT_PIPELINE', pipelineId: 'longlive' })
    actor.send({ type: 'SET_MODE', mode: 'webcam' })

    const snapshot = actor.getSnapshot()
    expect(snapshot.context.baseUrl).toBe('https://scope.xode.live')
    expect(snapshot.context.selectedPipelineId).toBe('longlive')
    expect(snapshot.context.mode).toBe('webcam')
  })
})
