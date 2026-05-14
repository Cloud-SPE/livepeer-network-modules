import type {
  IceServersResponse,
  ModelStatusResponse,
  PipelineSchemasResponse,
  PipelineStatusResponse,
  WebRTCOfferResponse,
} from '../types'

interface ApiRequestOptions extends RequestInit {
  logRequest?: boolean
  logResponse?: boolean
}

interface GatewaySessionOpenResponse {
  session_id?: string
}

interface GatewaySessionState {
  supported: boolean | null
  sessionId: string | null
  opening: Promise<string | null> | null
}

export interface StatusSnapshot {
  pipelineStatus: PipelineStatusResponse
  modelStatus: ModelStatusResponse
}

function trimBaseUrl(baseUrl: string) {
  return baseUrl.trim().replace(/\/+$/, '')
}

const gatewaySessionByBaseUrl = new Map<string, GatewaySessionState>()

function resolveBaseUrl(baseUrl: string) {
  const normalizedBaseUrl = trimBaseUrl(baseUrl)
  if (normalizedBaseUrl) {
    return normalizedBaseUrl
  }

  if (typeof window !== 'undefined' && window.location?.origin) {
    return window.location.origin.replace(/\/+$/, '')
  }

  throw new Error('Base URL is required.')
}

export function stringifyPayload(payload: unknown) {
  return typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2)
}

function shouldUseGatewaySession(path: string) {
  return path.startsWith('/api/v1/')
}

function gatewayStateFor(baseUrl: string): GatewaySessionState {
  const normalizedBaseUrl = resolveBaseUrl(baseUrl)
  let state = gatewaySessionByBaseUrl.get(normalizedBaseUrl)
  if (!state) {
    state = {
      supported: null,
      sessionId: null,
      opening: null,
    }
    gatewaySessionByBaseUrl.set(normalizedBaseUrl, state)
  }
  return state
}

async function ensureGatewaySession(baseUrl: string): Promise<string | null> {
  const normalizedBaseUrl = resolveBaseUrl(baseUrl)
  const state = gatewayStateFor(normalizedBaseUrl)

  if (state.supported === false) return null
  if (state.sessionId) return state.sessionId
  if (state.opening) return state.opening

  state.opening = (async () => {
    const response = await fetch(`${normalizedBaseUrl}/v1/sessions`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: '{}',
    })

    const contentType = response.headers.get('content-type') || ''
    const body = contentType.includes('application/json')
      ? await response.json()
      : await response.text()

    if (response.status === 404 || response.status === 405) {
      state.supported = false
      state.sessionId = null
      return null
    }

    if (!response.ok) {
      throw new Error(`/v1/sessions failed (${response.status}): ${stringifyPayload(body)}`)
    }

    const sessionId = (body as GatewaySessionOpenResponse).session_id
    if (!sessionId) {
      throw new Error(`/v1/sessions succeeded but no session_id was returned: ${stringifyPayload(body)}`)
    }

    state.supported = true
    state.sessionId = sessionId
    return sessionId
  })()

  try {
    return await state.opening
  } finally {
    state.opening = null
  }
}

function clearGatewaySession(baseUrl: string) {
  const state = gatewayStateFor(baseUrl)
  state.sessionId = null
  state.opening = null
}

export async function closeGatewaySession(baseUrl: string): Promise<void> {
  const normalizedBaseUrl = resolveBaseUrl(baseUrl)
  const state = gatewayStateFor(normalizedBaseUrl)
  const sessionId = state.sessionId
  clearGatewaySession(normalizedBaseUrl)

  if (!sessionId || state.supported !== true) return

  try {
    await fetch(`${normalizedBaseUrl}/v1/sessions/${encodeURIComponent(sessionId)}/close`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: '{}',
    })
  } catch {
    // Best-effort cleanup only; local UI shutdown should not fail on this.
  }
}

async function sendApiRequest<T = unknown>(
  baseUrl: string,
  path: string,
  options: ApiRequestOptions,
  retryOnMissingSession: boolean,
): Promise<{ status: number; body: T }> {
  const normalizedBaseUrl = resolveBaseUrl(baseUrl)
  const headers = new Headers(options.headers)
  if (!headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  if (shouldUseGatewaySession(path)) {
    const gatewaySessionId = await ensureGatewaySession(normalizedBaseUrl)
    if (gatewaySessionId) {
      headers.set('X-Daydream-Session', gatewaySessionId)
    }
  }

  const response = await fetch(`${normalizedBaseUrl}${path}`, {
    headers,
    ...options,
  })

  const contentType = response.headers.get('content-type') || ''
  const body = contentType.includes('application/json')
    ? await response.json()
    : await response.text()

  if (
    retryOnMissingSession &&
    shouldUseGatewaySession(path) &&
    response.status === 409 &&
    typeof body === 'object' &&
    body !== null &&
    'error' in body &&
    body.error === 'no_session'
  ) {
    clearGatewaySession(normalizedBaseUrl)
    return sendApiRequest<T>(normalizedBaseUrl, path, options, false)
  }

  if (!response.ok) {
    throw new Error(`${path} failed (${response.status}): ${stringifyPayload(body)}`)
  }

  return {
    status: response.status,
    body: body as T,
  }
}

export async function apiRequest<T = unknown>(
  baseUrl: string,
  path: string,
  options: ApiRequestOptions = {},
): Promise<{ status: number; body: T }> {
  return sendApiRequest<T>(baseUrl, path, options, true)
}

export async function fetchSchemas(baseUrl: string) {
  return apiRequest<PipelineSchemasResponse>(baseUrl, '/api/v1/pipelines/schemas')
}

export async function fetchPipelineStatus(baseUrl: string) {
  return apiRequest<PipelineStatusResponse>(baseUrl, '/api/v1/pipeline/status')
}

export async function fetchModelStatus(baseUrl: string, pipelineId: string) {
  return apiRequest<ModelStatusResponse>(
    baseUrl,
    `/api/v1/models/status?pipeline_id=${encodeURIComponent(pipelineId)}`,
  )
}

export async function fetchStatusSnapshot(baseUrl: string, pipelineId: string): Promise<StatusSnapshot> {
  const [pipelineStatusResponse, modelStatusResponse] = await Promise.all([
    fetchPipelineStatus(baseUrl),
    fetchModelStatus(baseUrl, pipelineId),
  ])

  return {
    pipelineStatus: pipelineStatusResponse.body,
    modelStatus: modelStatusResponse.body,
  }
}

export async function startModelDownload(baseUrl: string, pipelineId: string) {
  return apiRequest<{ message?: string }>(baseUrl, '/api/v1/models/download', {
    method: 'POST',
    body: JSON.stringify({ pipeline_id: pipelineId }),
  })
}

export async function loadPipeline(
  baseUrl: string,
  pipelineId: string,
  loadParams: Record<string, unknown>,
) {
  return apiRequest<{ message?: string }>(baseUrl, '/api/v1/pipeline/load', {
    method: 'POST',
    body: JSON.stringify({
      pipeline_ids: [pipelineId],
      load_params: loadParams,
    }),
  })
}

export async function waitForPipelineLoaded(
  baseUrl: string,
  pipelineId: string,
  timeoutMs = 300000,
  intervalMs = 2000,
) {
  const started = Date.now()

  while (Date.now() - started < timeoutMs) {
    const response = await fetchPipelineStatus(baseUrl)
    const status = response.body

    if (status.status === 'loaded' && status.pipeline_id === pipelineId) {
      return status
    }

    if (status.status === 'error') {
      throw new Error(status.error || 'Pipeline failed to load.')
    }

    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }

  throw new Error('Timed out waiting for pipeline to load.')
}

export async function fetchIceServers(baseUrl: string) {
  return apiRequest<IceServersResponse>(baseUrl, '/api/v1/webrtc/ice-servers')
}

export async function createWebRtcOffer(
  baseUrl: string,
  payload: Record<string, unknown>,
) {
  return apiRequest<WebRTCOfferResponse>(baseUrl, '/api/v1/webrtc/offer', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export async function patchIceCandidate(
  baseUrl: string,
  sessionId: string,
  candidate: RTCIceCandidate,
) {
  return apiRequest(baseUrl, `/api/v1/webrtc/offer/${sessionId}`, {
    method: 'PATCH',
    body: JSON.stringify({
      candidates: [
        {
          candidate: candidate.candidate,
          sdpMid: candidate.sdpMid,
          sdpMLineIndex: candidate.sdpMLineIndex,
        },
      ],
    }),
  })
}
