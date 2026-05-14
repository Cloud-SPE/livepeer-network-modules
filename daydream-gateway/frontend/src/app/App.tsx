import { useActorRef, useSelector } from '@xstate/react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  buildPromptWithLook,
  denoisingPresets,
  getAdaptedLookPreset,
  getQualityPresetForValue,
  lookPresetDefinitions,
} from '../looks'
import { studioMachine } from './studioMachine'
import type { PipelineUiProperty, PromptInterpolation } from '../types'

const loadParamOrder = ['height', 'width', 'base_seed', 'vae_type', 'quantization', 'manage_cache']
const resolutionPresets = [
  { label: 'Square 512', width: 512, height: 512 },
  { label: 'Scope 576x320', width: 576, height: 320 },
  { label: 'HD 768x432', width: 768, height: 432 },
] as const
const sectionPrefsKey = 'scope-studio-section-visibility'
const helpModePrefsKey = 'scope-studio-help-mode'
const defaultSectionVisibility = {
  pipelineProfile: true,
  loadConfig: true,
  creativeSteering: true,
  runtimeControls: true,
  diagnostics: true,
} as const

type SectionVisibility = typeof defaultSectionVisibility

function getSelectedSchema(snapshot: ReturnType<typeof studioMachine.getInitialSnapshot>) {
  return snapshot.context.pipelineSchemas[snapshot.context.selectedPipelineId] || null
}

function getLook(snapshot: ReturnType<typeof studioMachine.getInitialSnapshot>) {
  return getAdaptedLookPreset(snapshot.context.selectedLookId, {
    mode: snapshot.context.mode,
    pipelineId: snapshot.context.selectedPipelineId,
    vaceEnabled: Boolean(snapshot.context.loadValues.vace_enabled ?? getSelectedSchema(snapshot)?.supports_vace),
  })
}

function InfoTip({ text }: { text: string }) {
  return (
    <span className="tooltip-wrap">
      <button
        type="button"
        className="tooltip-trigger"
        aria-label={text}
      >
        i
      </button>
      <span className="tooltip-bubble" role="tooltip">
        {text}
      </span>
    </span>
  )
}

function ControlField({
  label,
  help,
  tooltip,
  children,
}: {
  label: string
  help?: string
  tooltip?: string
  children: ReactNode
}) {
  return (
    <label className="field">
      <span className="field-label">
        <span>{label}</span>
        {tooltip ? <InfoTip text={tooltip} /> : null}
      </span>
      {children}
      {help ? <small>{help}</small> : null}
    </label>
  )
}

function ToggleField({
  label,
  help,
  tooltip,
  checked,
  onChange,
}: {
  label: string
  help?: string
  tooltip?: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="toggle-field">
      <span>
        <span className="field-label">
          <strong>{label}</strong>
          {tooltip ? <InfoTip text={tooltip} /> : null}
        </span>
        {help ? <small>{help}</small> : null}
      </span>
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} />
    </label>
  )
}

function loadSectionVisibility(): SectionVisibility {
  if (typeof window === 'undefined') {
    return { ...defaultSectionVisibility }
  }

  try {
    const parsed = JSON.parse(window.localStorage.getItem(sectionPrefsKey) || 'null') as Partial<SectionVisibility> | null
    return {
      ...defaultSectionVisibility,
      ...(parsed || {}),
    }
  } catch {
    return { ...defaultSectionVisibility }
  }
}

function loadHelpMode(): boolean {
  if (typeof window === 'undefined') {
    return false
  }

  return window.localStorage.getItem(helpModePrefsKey) === 'true'
}

function CollapsibleBlock({
  title,
  subtitle,
  tooltip,
  actions,
  open,
  onToggle,
  children,
  className = '',
}: {
  title: string
  subtitle: string
  tooltip?: string
  actions?: React.ReactNode
  open: boolean
  onToggle: () => void
  children: ReactNode
  className?: string
}) {
  return (
    <div className={className}>
      <div className="collapsible-header">
        <button
          type="button"
          className="collapsible-toggle"
          aria-expanded={open}
          onClick={onToggle}
        >
          <span className="collapsible-copy">
            <span className="field-label">
              <strong>{title}</strong>
              {tooltip ? <InfoTip text={tooltip} /> : null}
            </span>
            <small>{subtitle}</small>
          </span>
          <span className="collapsible-chevron" aria-hidden="true">
            {open ? '−' : '+'}
          </span>
        </button>
        {actions ? <div className="collapsible-actions">{actions}</div> : null}
      </div>
      {open ? <div className="collapsible-body">{children}</div> : null}
    </div>
  )
}

function renderPropertyControl(
  propertyName: string,
  property: PipelineUiProperty,
  value: unknown,
  onChange: (value: unknown) => void,
) {
  const label = property.ui?.label || property.title || propertyName
  const help = property.description
  const tooltip = property.description

  if (property.enum) {
    return (
      <ControlField key={propertyName} label={label} help={help} tooltip={tooltip}>
        <select value={value === undefined || value === null ? '__null__' : String(value)} onChange={(event) => onChange(event.target.value === '__null__' ? undefined : event.target.value)}>
          {[...property.enum, null].map((option) => (
            <option key={String(option)} value={option === null ? '__null__' : String(option)}>
              {option === null ? 'default' : String(option)}
            </option>
          ))}
        </select>
      </ControlField>
    )
  }

  const enumOption = property.anyOf?.find((option) => Array.isArray(option.enum))
  if (enumOption?.enum) {
    return (
      <ControlField key={propertyName} label={label} help={help} tooltip={tooltip}>
        <select value={value === undefined || value === null ? '__null__' : String(value)} onChange={(event) => onChange(event.target.value === '__null__' ? undefined : event.target.value)}>
          {[...enumOption.enum, null].map((option) => (
            <option key={String(option)} value={option === null ? '__null__' : String(option)}>
              {option === null ? 'default' : String(option)}
            </option>
          ))}
        </select>
      </ControlField>
    )
  }

  if (property.type === 'boolean') {
    return (
      <ToggleField
        key={propertyName}
        label={label}
        help={help}
        tooltip={tooltip}
        checked={Boolean(value)}
        onChange={onChange}
      />
    )
  }

  const isNumeric = property.type === 'integer' || property.type === 'number'
  return (
    <ControlField key={propertyName} label={label} help={help} tooltip={tooltip}>
      <input
        type={isNumeric ? 'number' : 'text'}
        value={value === undefined || value === null ? '' : String(value)}
        min={property.minimum}
        max={property.maximum}
        step={property.type === 'integer' ? 1 : 'any'}
        onChange={(event) => onChange(isNumeric ? (event.target.value === '' ? undefined : Number(event.target.value)) : event.target.value)}
      />
    </ControlField>
  )
}

export function App() {
  const actorRef = useActorRef(studioMachine)
  const snapshot = useSelector(actorRef, (state) => state)
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const localVideoRef = useRef<HTMLVideoElement | null>(null)
  const [sectionVisibility, setSectionVisibility] = useState<SectionVisibility>(() => loadSectionVisibility())
  const [helpMode, setHelpMode] = useState<boolean>(() => loadHelpMode())

  const selectedSchema = getSelectedSchema(snapshot)
  const currentLook = getLook(snapshot)
  const displayPrompt = buildPromptWithLook(snapshot.context.promptBase, currentLook)
  const qualityPreset = getQualityPresetForValue(snapshot.context.macros.quality)
  const isSessionActive = snapshot.matches('sessionActive')
  const canStop = snapshot.can({ type: 'STOP_SESSION' })
  const canStart = snapshot.can({ type: 'START_SESSION' })
  const canLoad = snapshot.can({ type: 'LOAD_PIPELINE' })
  const canDownload = snapshot.can({ type: 'DOWNLOAD_MODELS' })

  const pipelineMeta = useMemo(() => {
    if (!selectedSchema) {
      return []
    }

    return [
      ['Modes', (selectedSchema.supported_modes || []).join(', ') || 'unknown'],
      ['Inputs', (selectedSchema.inputs || []).join(', ') || 'none'],
      ['Outputs', (selectedSchema.outputs || []).join(', ') || 'none'],
      ['VRAM', selectedSchema.estimated_vram_gb ? `${selectedSchema.estimated_vram_gb} GB` : 'n/a'],
      ['VACE', selectedSchema.supports_vace ? 'supported' : 'no'],
      ['LoRA', selectedSchema.supports_lora ? 'supported' : 'no'],
      ['Quantization', selectedSchema.supports_quantization ? 'supported' : 'no'],
    ]
  }, [selectedSchema])

  useEffect(() => {
    if (videoRef.current) {
      videoRef.current.srcObject = snapshot.context.remoteStream
    }
  }, [snapshot.context.remoteStream])

  useEffect(() => {
    if (localVideoRef.current) {
      localVideoRef.current.srcObject = snapshot.context.localStream
    }
  }, [snapshot.context.localStream])

  const loadProperties = selectedSchema?.config_schema?.properties || {}
  const basicLoadKeys = loadParamOrder.filter((key) => loadProperties[key])
  const advancedLoadKeys = Object.keys(loadProperties).filter((key) => !basicLoadKeys.includes(key))
  const visibleLoadKeys =
    snapshot.context.loadControlMode === 'basic'
      ? basicLoadKeys
      : [...basicLoadKeys, ...advancedLoadKeys]
  const loadedPipelineId = snapshot.context.pipelineStatus.loadedPipelineId
  const loadedPipelineLabel = loadedPipelineId || 'none'
  const pipelineMismatch = Boolean(
    loadedPipelineId &&
      loadedPipelineId !== snapshot.context.selectedPipelineId,
  )
  const backendReady =
    snapshot.context.modelStatus.downloaded &&
    loadedPipelineId === snapshot.context.selectedPipelineId

  useEffect(() => {
    window.localStorage.setItem(sectionPrefsKey, JSON.stringify(sectionVisibility))
  }, [sectionVisibility])

  useEffect(() => {
    window.localStorage.setItem(helpModePrefsKey, String(helpMode))
  }, [helpMode])

  function toggleSection(key: keyof SectionVisibility) {
    setSectionVisibility((current) => ({
      ...current,
      [key]: !current[key],
    }))
  }

  return (
    <main className={`studio-shell ${helpMode ? 'help-mode' : ''}`}>
      <header className="studio-header">
        <div>
          <p className="eyebrow">Scope Studio</p>
          <h1>Realtime generative session control</h1>
        </div>
        <div className="header-status">
          <div className="status-stack">
            <button
              type="button"
              className={`ghost compact ${helpMode ? 'active-mode-toggle' : ''}`}
              onClick={() => setHelpMode((current) => !current)}
            >
              {helpMode ? 'Help Mode On' : 'Help Mode'}
            </button>
            <span className="status-detail">show guidance on all tooltip-enabled controls</span>
          </div>
          <div className="status-stack">
            <span id="pipeline-badge" className="status-pill" data-state={snapshot.context.pipelineStatus.badge}>
              {snapshot.context.pipelineStatus.badge}
            </span>
            <span className="status-detail">{snapshot.context.pipelineStatus.stage}</span>
          </div>
          <div className="status-stack">
            <span id="models-badge" className="status-pill" data-state={snapshot.context.modelStatus.badge}>
              {snapshot.context.modelStatus.badge}
            </span>
            <span className="status-detail">{snapshot.context.modelStatus.stage}</span>
          </div>
          <div className="status-stack">
            <span className="status-pill" data-state={snapshot.context.resourceActivity === 'idle' ? 'loaded' : 'loading'}>
              {snapshot.context.resourceActivity}
            </span>
            <span className="status-detail">resource activity</span>
          </div>
        </div>
      </header>

      {snapshot.context.banner ? (
        <section className="session-banner" data-state={snapshot.context.banner.tone}>
          {snapshot.context.banner.message}
        </section>
      ) : null}

      <section className="studio-grid">
        <aside className="panel studio-column">
          <div className="section-header">
            <h2>Setup</h2>
            <p>Server, pipeline, load config, and session launch.</p>
          </div>

          <div className="field-row">
            <ControlField
              label="Pipeline"
              tooltip="Selects the primary Scope pipeline to load and run for this session."
            >
              <select
                value={snapshot.context.selectedPipelineId}
                onChange={(event) => {
                  actorRef.send({ type: 'SELECT_PIPELINE', pipelineId: event.target.value })
                  actorRef.send({ type: 'REFRESH_REQUEST' })
                }}
              >
                {snapshot.context.availablePipelines.map((pipelineId) => (
                  <option key={pipelineId} value={pipelineId}>
                    {pipelineId}
                  </option>
                ))}
              </select>
            </ControlField>
            <button className="ghost compact" onClick={() => actorRef.send({ type: 'REFRESH_REQUEST' })}>
              Refresh
            </button>
          </div>

          <div className="field-row">
            <ControlField
              label="Mode"
              tooltip="Text to video creates output without a live input feed. Webcam to video sends your camera frames to Scope for live video-to-video inference."
            >
              <select
                value={snapshot.context.mode}
                onChange={(event) => actorRef.send({ type: 'SET_MODE', mode: event.target.value as 'receive' | 'webcam' })}
              >
                <option value="receive">Text to video</option>
                <option value="webcam">Webcam to video</option>
              </select>
            </ControlField>
            <button className="ghost compact" onClick={() => actorRef.send({ type: 'TOGGLE_CAMERA' })}>
              {snapshot.context.localStream ? 'Disable webcam' : 'Enable webcam'}
            </button>
          </div>

          <div className="meta-card">
            <div className="block-header">
              <h3>Backend status</h3>
              <p>What the UI selected versus what the Scope backend is actually ready to run.</p>
            </div>
            <div className="meta-grid">
              <div className="meta-item">
                <span>Selected</span>
                <strong>{snapshot.context.selectedPipelineId}</strong>
              </div>
              <div className="meta-item">
                <span>Models</span>
                <strong>{snapshot.context.modelStatus.badge}</strong>
              </div>
              <div className="meta-item">
                <span>Loaded</span>
                <strong>{loadedPipelineLabel}</strong>
              </div>
              <div className="meta-item">
                <span>Session readiness</span>
                <strong>{backendReady ? 'ready' : 'not ready'}</strong>
              </div>
            </div>
            {pipelineMismatch ? (
              <div className="inline-status-banner" data-state="warning">
                Backend currently has <strong>{loadedPipelineId}</strong> loaded. Load the selected pipeline before starting.
              </div>
            ) : null}
            {!snapshot.context.modelStatus.downloaded ? (
              <div className="inline-status-banner" data-state="warning">
                Models for <strong>{snapshot.context.selectedPipelineId}</strong> are not fully downloaded yet.
              </div>
            ) : null}
            {backendReady ? (
              <div className="inline-status-banner" data-state="success">
                Selected pipeline, downloaded models, and loaded backend pipeline are aligned.
              </div>
            ) : null}
          </div>

          <div className="meta-card">
            <CollapsibleBlock
              title="Pipeline profile"
              subtitle="Capabilities and readiness for the selected pipeline."
              tooltip="Summary of what the selected pipeline supports, including modes, features, and VRAM expectations."
              open={sectionVisibility.pipelineProfile}
              onToggle={() => toggleSection('pipelineProfile')}
              actions={(
                <button className="ghost compact" disabled={!canDownload} onClick={() => actorRef.send({ type: 'DOWNLOAD_MODELS' })}>
                  Download models
                </button>
              )}
            >
              <div className="meta-grid">
                {pipelineMeta.length ? (
                  pipelineMeta.map(([label, value]) => (
                    <div key={label} className="meta-item">
                      <span>{label}</span>
                      <strong>{value}</strong>
                    </div>
                  ))
                ) : (
                  <p className="empty-state">No schema loaded.</p>
                )}
              </div>
            </CollapsibleBlock>
          </div>

          <div className="control-block">
            <CollapsibleBlock
              title="Load config"
              subtitle="Settings applied when Scope loads the selected pipeline."
              tooltip="These values affect pipeline initialization and are applied when the backend loads or reloads the selected pipeline."
              open={sectionVisibility.loadConfig}
              onToggle={() => toggleSection('loadConfig')}
            >
              <div className="mode-toggle-row">
                <button
                  className={`ghost compact ${snapshot.context.loadControlMode === 'basic' ? 'active-mode-toggle' : ''}`}
                  type="button"
                  onClick={() => actorRef.send({ type: 'SET_LOAD_CONTROL_MODE', value: 'basic' })}
                >
                  Basic
                </button>
                <button
                  className={`ghost compact ${snapshot.context.loadControlMode === 'advanced' ? 'active-mode-toggle' : ''}`}
                  type="button"
                  onClick={() => actorRef.send({ type: 'SET_LOAD_CONTROL_MODE', value: 'advanced' })}
                >
                  Advanced
                </button>
              </div>
              <div className="preset-grid">
                {resolutionPresets.map((preset) => (
                  <button
                    key={preset.label}
                    type="button"
                    className="preset-chip"
                    onClick={() => actorRef.send({ type: 'APPLY_LOAD_PRESET', width: preset.width, height: preset.height })}
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
              <div className="control-grid">
                {visibleLoadKeys.map((key) =>
                  renderPropertyControl(key, loadProperties[key], snapshot.context.loadValues[key], (value) =>
                    actorRef.send({ type: 'SET_LOAD_VALUE', key, value }),
                  ),
                )}
                {selectedSchema?.supports_vace ? (
                  <ToggleField
                    label="Reference guidance (VACE)"
                    help="Enable VACE-aware conditioning for supported pipelines."
                    tooltip="Turns on VACE reference guidance for pipelines that support it. Useful when you want stronger structural or style conditioning."
                    checked={Boolean(snapshot.context.loadValues.vace_enabled ?? true)}
                    onChange={(checked) => actorRef.send({ type: 'SET_LOAD_VALUE', key: 'vace_enabled', value: checked })}
                  />
                ) : null}
              </div>
            </CollapsibleBlock>
          </div>

          <div className="action-cluster">
            <button className="accent" disabled={!canLoad} onClick={() => actorRef.send({ type: 'LOAD_PIPELINE' })}>
              {snapshot.matches('loadingPipeline') ? 'Loading pipeline...' : 'Load pipeline'}
            </button>
            <button className="accent" disabled={!canStart} onClick={() => actorRef.send({ type: 'START_SESSION' })}>
              {snapshot.matches('preparingSession') ? 'Preparing session...' : 'Start session'}
            </button>
            <button disabled={!canStop} onClick={() => actorRef.send({ type: 'STOP_SESSION' })}>
              Stop session
            </button>
          </div>
        </aside>

        <section className="studio-stage">
          <div className="panel monitor-panel">
            <div className="output-header">
              <div>
                <h2>Monitor</h2>
                <p className="subtle">Remote Scope output and live session health.</p>
              </div>
              <span
                className="status-pill"
                data-state={
                  snapshot.context.sessionPhase === 'streaming' || snapshot.context.sessionPhase === 'connected'
                    ? 'loaded'
                    : snapshot.context.sessionPhase === 'failed' || snapshot.context.sessionPhase === 'serverError'
                      ? 'error'
                      : snapshot.context.sessionPhase === 'idle' || snapshot.context.sessionPhase === 'stopped'
                        ? 'unknown'
                        : 'loading'
                }
              >
                {snapshot.context.sessionLabel}
              </span>
            </div>

            <div className="monitor-stack">
              {snapshot.context.mode === 'webcam' ? (
                <section className="input-panel">
                  <div className="mini-header">
                    <h3>Live input</h3>
                    <span className="status-pill">
                      {snapshot.context.localStream ? 'camera live' : snapshot.matches('requestingCamera') ? 'requesting camera' : 'inactive'}
                    </span>
                  </div>
                  <video ref={localVideoRef} autoPlay muted playsInline />
                </section>
              ) : null}

              <section className="remote-panel">
                <video ref={videoRef} autoPlay muted playsInline />
              </section>
            </div>

            <div className="diagnostics" aria-live="polite">
              {Object.entries(snapshot.context.diagnostics).map(([key, value]) => (
                <div key={key} className="diagnostic">
                  <span>{key.charAt(0).toUpperCase() + key.slice(1)}</span>
                  <strong>{value}</strong>
                </div>
              ))}
            </div>
          </div>

          <section className="panel diagnostics-panel">
            <CollapsibleBlock
              title="Diagnostics"
              subtitle="Machine-owned logs and raw API/session output."
              tooltip="Raw frontend logs, API responses, and session events. Useful for debugging startup, load, and WebRTC failures."
              open={sectionVisibility.diagnostics}
              onToggle={() => toggleSection('diagnostics')}
              actions={(
                <div className="action-cluster tight">
                  <button className="ghost compact" onClick={() => actorRef.send({ type: 'REFRESH_REQUEST' })}>
                    Refresh
                  </button>
                  <button className="ghost compact" onClick={() => actorRef.send({ type: 'BOOT' })}>
                    Reset App
                  </button>
                  <button className="ghost compact" onClick={() => actorRef.send({ type: 'CLEAR_LOGS' })}>
                    Clear
                  </button>
                </div>
              )}
            >
              <pre>{snapshot.context.logs.join('\n\n')}</pre>
              <details>
                <summary>Resolved prompt</summary>
                <pre>{displayPrompt}</pre>
              </details>
            </CollapsibleBlock>
          </section>
        </section>

        <aside className="panel studio-column">
          <div className="section-header">
            <h2>Creative</h2>
            <p>Prompting, transitions, and runtime control.</p>
          </div>

          <ControlField
            label="Prompt"
            tooltip="Primary text instruction sent to the active Scope session. Update it live during streaming if the data channel is connected."
          >
            <textarea
              rows={5}
              spellCheck={false}
              value={snapshot.context.promptBase}
              onChange={(event) => actorRef.send({ type: 'SET_PROMPT', value: event.target.value })}
            />
          </ControlField>

          <div className="field-row double">
            <ControlField
              label="Blend method"
              tooltip="Controls how prompt transitions are interpolated. Linear is simpler; slerp can produce smoother semantic blends."
            >
              <select
                value={snapshot.context.promptInterpolation}
                onChange={(event) => actorRef.send({ type: 'SET_PROMPT_INTERPOLATION', value: event.target.value as PromptInterpolation })}
              >
                <option value="linear">linear</option>
                <option value="slerp">slerp</option>
              </select>
            </ControlField>
            <ControlField
              label="Transition steps"
              tooltip="How many intermediate steps Scope uses when smoothly transitioning from one prompt state to another."
            >
              <input
                type="number"
                min={0}
                step={1}
                value={snapshot.context.transitionSteps}
                onChange={(event) => actorRef.send({ type: 'SET_TRANSITION_STEPS', value: Number(event.target.value || 0) })}
              />
            </ControlField>
          </div>

          <div className="control-block macro-block">
            <CollapsibleBlock
              title="Creative steering"
              subtitle="High-level controls mapped onto Scope parameters for faster tuning."
              tooltip="Operator-friendly macro controls that map onto lower-level Scope runtime parameters."
              open={sectionVisibility.creativeSteering}
              onToggle={() => toggleSection('creativeSteering')}
            >
              <div className="looks-block">
                <div className="block-header">
                  <h3>Looks</h3>
                  <p>Preset visual directions that reshape the prompt and runtime.</p>
                </div>
                <div className="look-grid">
                  {lookPresetDefinitions.map((look) => {
                    const selected = snapshot.context.selectedLookId === look.id
                    const recommendedPipeline = getAdaptedLookPreset(look.id, {
                      mode: snapshot.context.mode,
                      pipelineId: snapshot.context.selectedPipelineId,
                      vaceEnabled: Boolean(snapshot.context.loadValues.vace_enabled ?? selectedSchema?.supports_vace),
                    })?.recommendedPipeline

                    return (
                      <button
                        key={look.id}
                        type="button"
                        className={`look-card ${selected ? 'active-look-card' : ''}`}
                        onClick={() => actorRef.send({ type: 'SELECT_LOOK', lookId: look.id })}
                      >
                        <strong>{look.label}</strong>
                        <span>{look.note}</span>
                        <em className="look-recommendation" data-state={recommendedPipeline === snapshot.context.selectedPipelineId ? 'matched' : 'recommended'}>
                          {recommendedPipeline === snapshot.context.selectedPipelineId
                            ? 'using recommended pipeline'
                            : `best with ${recommendedPipeline}`}
                        </em>
                      </button>
                    )
                  })}
                </div>
              </div>
              <div className="control-grid">
                <ControlField
                  label="Speed vs quality"
                  tooltip="Shifts the denoising schedule toward faster response or higher detail and quality."
                >
                  <div className="range-stack">
                    <input
                      type="range"
                      min={0}
                      max={100}
                      step={1}
                      value={snapshot.context.macros.quality}
                      onChange={(event) => actorRef.send({ type: 'SET_MACRO', key: 'quality', value: Number(event.target.value) })}
                    />
                    <div className="range-meta">
                      <strong>{qualityPreset.label}</strong>
                      <small>maps to denoising schedule</small>
                    </div>
                  </div>
                </ControlField>
                <ControlField
                  label="Reference strength"
                  tooltip="Controls how strongly reference guidance influences the result. Higher values preserve reference traits more aggressively."
                >
                  <div className="range-stack">
                    <input
                      type="range"
                      min={0}
                      max={100}
                      step={1}
                      value={snapshot.context.macros.reference}
                      onChange={(event) => actorRef.send({ type: 'SET_MACRO', key: 'reference', value: Number(event.target.value) })}
                    />
                    <div className="range-meta">
                      <strong>
                        {snapshot.context.macros.reference < 34 ? 'Subtle' : snapshot.context.macros.reference < 67 ? 'Balanced' : 'Strong'}
                      </strong>
                      <small>maps to VACE context scale</small>
                    </div>
                  </div>
                </ControlField>
                <ControlField
                  label="Motion stability"
                  tooltip="Biases generation toward steadier temporal behavior. Higher values usually reduce flicker but can lower visual change."
                >
                  <div className="range-stack">
                    <input
                      type="range"
                      min={0}
                      max={100}
                      step={1}
                      value={snapshot.context.macros.stability}
                      onChange={(event) => actorRef.send({ type: 'SET_MACRO', key: 'stability', value: Number(event.target.value) })}
                    />
                    <div className="range-meta">
                      <strong>
                        {snapshot.context.macros.stability < 34 ? 'Loose' : snapshot.context.macros.stability < 67 ? 'Stable' : 'Locked'}
                      </strong>
                      <small>maps to noise scale and controller</small>
                    </div>
                  </div>
                </ControlField>
              </div>
            </CollapsibleBlock>
          </div>

          <div className="action-cluster">
            <button className="ghost" disabled={!isSessionActive} onClick={() => actorRef.send({ type: 'SEND_PROMPT_UPDATE' })}>
              Update prompt
            </button>
            <button className="ghost" disabled={!isSessionActive} onClick={() => actorRef.send({ type: 'SEND_PROMPT_TRANSITION' })}>
              Smooth transition
            </button>
            <button className="ghost" disabled={!isSessionActive} onClick={() => actorRef.send({ type: 'SEND_RUNTIME_UPDATE' })}>
              Apply live controls
            </button>
          </div>

          <div className="control-block">
            <CollapsibleBlock
              title="Runtime controls"
              subtitle="Parameters sent in the session offer and over the live data channel."
              tooltip="Live generation parameters that affect the active session without necessarily reloading the pipeline."
              open={sectionVisibility.runtimeControls}
              onToggle={() => toggleSection('runtimeControls')}
            >
              <div className="mode-toggle-row">
                <button
                  className={`ghost compact ${snapshot.context.runtimeControlMode === 'basic' ? 'active-mode-toggle' : ''}`}
                  type="button"
                  onClick={() => actorRef.send({ type: 'SET_RUNTIME_CONTROL_MODE', value: 'basic' })}
                >
                  Basic
                </button>
                <button
                  className={`ghost compact ${snapshot.context.runtimeControlMode === 'advanced' ? 'active-mode-toggle' : ''}`}
                  type="button"
                  onClick={() => actorRef.send({ type: 'SET_RUNTIME_CONTROL_MODE', value: 'advanced' })}
                >
                  Advanced
                </button>
              </div>
              <div className="preset-grid">
                {denoisingPresets.map((preset) => (
                  <button
                    key={preset.label}
                    type="button"
                    className={`preset-chip ${JSON.stringify(snapshot.context.runtimeValues.denoising_step_list) === JSON.stringify(preset.value) ? 'active-preset' : ''}`}
                    onClick={() => actorRef.send({ type: 'APPLY_RUNTIME_PRESET', values: [...preset.value] })}
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
              <div className="control-grid">
                <ControlField
                  label="Denoising steps"
                  help="Descending timesteps, comma separated."
                  tooltip="Explicit denoising schedule. More or denser steps can improve quality, but usually cost speed."
                >
                  <input
                    type="text"
                    value={snapshot.context.runtimeValues.denoising_step_list.join(', ')}
                    onChange={(event) =>
                      actorRef.send({
                        type: 'SET_RUNTIME_VALUE',
                        key: 'denoising_step_list',
                        value: event.target.value
                          .split(',')
                          .map((item) => Number(item.trim()))
                          .filter((item) => Number.isFinite(item)),
                      })
                    }
                  />
                </ControlField>
                <ControlField
                  label="Noise scale"
                  tooltip="Higher values push stronger frame-to-frame change. Lower values keep output steadier and more conservative."
                >
                  <div className="range-stack">
                    <input
                      type="range"
                      min={0}
                      max={1}
                      step={0.01}
                      value={snapshot.context.runtimeValues.noise_scale ?? 0.7}
                      onChange={(event) => actorRef.send({ type: 'SET_RUNTIME_VALUE', key: 'noise_scale', value: Number(event.target.value) })}
                    />
                    <div className="range-meta">
                      <strong>{(snapshot.context.runtimeValues.noise_scale ?? 0.7).toFixed(2)}</strong>
                      <small>stable / balanced / wild</small>
                    </div>
                  </div>
                </ControlField>
                <ControlField
                  label="VACE context scale"
                  tooltip="How strongly VACE reference/context guidance shapes the output. Increase it when you want stronger adherence to reference cues."
                >
                  <div className="range-stack">
                    <input
                      type="range"
                      min={0}
                      max={2}
                      step={0.05}
                      value={snapshot.context.runtimeValues.vace_context_scale ?? 1}
                      onChange={(event) => actorRef.send({ type: 'SET_RUNTIME_VALUE', key: 'vace_context_scale', value: Number(event.target.value) })}
                    />
                    <div className="range-meta">
                      <strong>{(snapshot.context.runtimeValues.vace_context_scale ?? 1).toFixed(2)}</strong>
                      <small>weak ref / balanced / strong ref</small>
                    </div>
                  </div>
                </ControlField>
                <ToggleField
                  label="Noise controller"
                  help="Enable dynamic noise control during generation."
                  tooltip="Lets the backend modulate noise during generation to improve stability and responsiveness."
                  checked={Boolean(snapshot.context.runtimeValues.noise_controller)}
                  onChange={(checked) => actorRef.send({ type: 'SET_RUNTIME_VALUE', key: 'noise_controller', value: checked })}
                />
                <ToggleField
                  label="Manage cache"
                  help="Keep cache management enabled during streaming."
                  tooltip="Keeps temporal/model cache management active for smoother continuity during streaming."
                  checked={Boolean(snapshot.context.runtimeValues.manage_cache)}
                  onChange={(checked) => actorRef.send({ type: 'SET_RUNTIME_VALUE', key: 'manage_cache', value: checked })}
                />
                {snapshot.context.runtimeControlMode === 'advanced' ? (
                  <ToggleField
                    label="Reset cache on apply"
                    help="Use when the model drifts or gets stuck."
                    tooltip="Clears cached temporal state when applying changes. Useful for drift or stuck output, but can cause a visible jump."
                    checked={Boolean(snapshot.context.runtimeValues.reset_cache)}
                    onChange={(checked) => actorRef.send({ type: 'SET_RUNTIME_VALUE', key: 'reset_cache', value: checked })}
                  />
                ) : null}
              </div>
            </CollapsibleBlock>
          </div>
        </aside>
      </section>
    </main>
  )
}
