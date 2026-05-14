import { createActor, fromCallback } from 'xstate'
import { describe, expect, it, vi } from 'vitest'
import { studioMachine } from './studioMachine'

const baseSchemas = {
  pipelines: {
    longlive: {
      supports_vace: true,
      config_schema: {
        properties: {
          width: { default: 576, type: 'integer' },
          height: { default: 320, type: 'integer' },
          manage_cache: { default: true, type: 'boolean' },
          denoising_steps: { default: [1000, 750, 500, 250], type: 'array' },
          noise_scale: { default: 0.7, type: 'number' },
          noise_controller: { default: true, type: 'boolean' },
          vace_context_scale: { default: 1, type: 'number' },
        },
      },
    },
    streamdiffusionv2: {
      supports_vace: true,
      config_schema: {
        properties: {
          width: { default: 512, type: 'integer' },
          height: { default: 512, type: 'integer' },
          manage_cache: { default: true, type: 'boolean' },
          denoising_steps: { default: [1000, 750], type: 'array' },
          noise_scale: { default: 0.65, type: 'number' },
          noise_controller: { default: true, type: 'boolean' },
          vace_context_scale: { default: 1, type: 'number' },
        },
      },
    },
  },
}

function createTestStudioMachine(options?: {
  cameraStream?: MediaStream
  onSessionStart?: () => void
}) {
  const resourcesStub = fromCallback(({ receive, sendBack }) => {
    receive((event: { type: string }) => {
      if (event.type === 'BOOTSTRAP') {
        sendBack({
          type: 'RESOURCES_STATUS',
          activity: 'bootstrapping',
        })
        sendBack({
          type: 'RESOURCES_BOOTSTRAPPED',
          output: {
            schemas: baseSchemas,
            snapshot: {
              pipelineStatus: { status: 'loaded', pipeline_id: 'longlive' },
              modelStatus: { downloaded: true },
              pipelineStatusUi: {
                badge: 'loaded',
                stage: 'Loaded: longlive',
                loadedPipelineId: 'longlive',
                loading: false,
              },
              modelStatusUi: {
                badge: 'downloaded',
                stage: 'longlive model files are ready.',
                downloaded: true,
                downloading: false,
              },
            },
          },
        })
      }

      if (event.type === 'REQUEST_CAMERA') {
        sendBack({
          type: 'RESOURCES_STATUS',
          activity: 'requestingCamera',
        })
        sendBack({
          type: 'RESOURCES_CAMERA_READY',
          stream: options?.cameraStream ?? ({ getTracks: () => [] } as unknown as MediaStream),
        })
      }

      if (event.type === 'PREPARE_SESSION') {
        sendBack({
          type: 'RESOURCES_STATUS',
          activity: 'preparingSession',
        })
        sendBack({
          type: 'RESOURCES_SESSION_PREPARED',
          output: {
            pipelineStatus: { status: 'loaded', pipeline_id: 'longlive' },
            modelStatus: null,
            pipelineStatusUi: {
              badge: 'loaded',
              stage: 'Loaded: longlive',
              loadedPipelineId: 'longlive',
              loading: false,
            },
          },
        })
      }
    })
  })

  const sessionStub = fromCallback(({ receive, sendBack }) => {
    receive((event: { type: string }) => {
      if (event.type === 'START') {
        options?.onSessionStart?.()
        sendBack({
          type: 'SERVER_STREAM_STOPPED',
          message: 'fatal stream stop',
          fatal: true,
        })
      }
    })
  })

  return studioMachine.provide({
    actors: {
      resourcesMachine: resourcesStub as never,
      sessionMachine: sessionStub as never,
    },
  })
}

async function flush() {
  await Promise.resolve()
  await Promise.resolve()
}

describe('studioMachine coordination', () => {
  it('recomputes editor defaults when the selected pipeline changes', async () => {
    const actor = createActor(createTestStudioMachine())
    actor.start()
    await flush()

    actor.send({ type: 'SELECT_PIPELINE', pipelineId: 'streamdiffusionv2' })
    await flush()

    const snapshot = actor.getSnapshot()
    expect(snapshot.context.selectedPipelineId).toBe('streamdiffusionv2')
    expect(snapshot.context.loadValues.width).toBe(512)
    expect(snapshot.context.loadValues.height).toBe(512)
    expect(snapshot.context.runtimeValues.manage_cache).toBe(true)
  })

  it('stops the active webcam stream when leaving webcam mode', async () => {
    const stop = vi.fn()
    const stream = {
      getTracks: () => [{ stop }],
    } as unknown as MediaStream

    const actor = createActor(createTestStudioMachine({ cameraStream: stream }))
    actor.start()
    await flush()

    actor.send({ type: 'SET_MODE', mode: 'webcam' })
    await flush()
    actor.send({ type: 'TOGGLE_CAMERA' })
    await flush()

    expect(actor.getSnapshot().context.localStream).toBe(stream)

    actor.send({ type: 'SET_MODE', mode: 'receive' })
    await flush()

    const snapshot = actor.getSnapshot()
    expect(stop).toHaveBeenCalledTimes(1)
    expect(snapshot.context.mode).toBe('receive')
    expect(snapshot.context.localStream).toBeNull()
  })

  it('moves to serverError when the session actor reports a fatal stop', async () => {
    const actor = createActor(createTestStudioMachine())
    actor.start()
    await flush()

    actor.send({ type: 'START_SESSION' })
    await flush()

    const snapshot = actor.getSnapshot()
    expect(snapshot.matches('serverError')).toBe(true)
    expect(snapshot.context.banner).toEqual({
      message: 'fatal stream stop',
      tone: 'error',
    })
    expect(snapshot.context.diagnostics.frames).toBe('server fatal stop')
  })
})
