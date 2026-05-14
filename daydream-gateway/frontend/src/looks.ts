import type {
  AdaptedLookPreset,
  LookContext,
  LookPresetDefinition,
} from './types'

export const denoisingPresets = [
  { label: 'Fast', value: [1000, 750] },
  { label: 'Balanced', value: [1000, 750, 500, 250] },
  { label: 'Detailed', value: [1000, 850, 700, 550, 400, 250] },
] as const

export const lookPresetDefinitions: LookPresetDefinition[] = [
  {
    id: 'cinematic',
    label: 'Cinematic',
    note: 'dramatic light, polished color, balanced motion',
    suffix: 'cinematic lighting, polished color grading, crisp detail, controlled motion',
    macros: { quality: 70, reference: 44, stability: 62 },
    transition: { steps: 10, interpolation: 'slerp' },
    recommendedPipelines: { receive: 'longlive', webcam: 'longlive' },
  },
  {
    id: 'stylized',
    label: 'Stylized',
    note: 'bolder texture, stronger transformation, graphic look',
    suffix: 'highly stylized visual treatment, bold texture, graphic shapes, expressive atmosphere',
    macros: { quality: 58, reference: 72, stability: 46 },
    transition: { steps: 8, interpolation: 'linear' },
    recommendedPipelines: { receive: 'streamdiffusionv2', webcam: 'streamdiffusionv2' },
  },
  {
    id: 'stable-realism',
    label: 'Stable realism',
    note: 'clean, grounded, less drift',
    suffix: 'natural lighting, realistic materials, grounded detail, stable motion',
    macros: { quality: 64, reference: 34, stability: 82 },
    transition: { steps: 6, interpolation: 'linear' },
    recommendedPipelines: { receive: 'longlive', webcam: 'longlive' },
  },
  {
    id: 'dreamlike',
    label: 'Dreamlike',
    note: 'soft atmosphere, diffusion, more surreal motion',
    suffix: 'dreamlike atmosphere, soft diffusion, luminous haze, surreal cinematic mood',
    macros: { quality: 78, reference: 60, stability: 36 },
    transition: { steps: 12, interpolation: 'slerp' },
    recommendedPipelines: { receive: 'longlive', webcam: 'streamdiffusionv2' },
  },
]

const lookContextPhrases = [
  'cinematic lighting, polished color grading, crisp detail, controlled motion',
  'highly stylized visual treatment, bold texture, graphic shapes, expressive atmosphere',
  'natural lighting, realistic materials, grounded detail, stable motion',
  'dreamlike atmosphere, soft diffusion, luminous haze, surreal cinematic mood',
  'preserve the webcam scene structure, identity, and spatial continuity',
  'generate a complete cinematic scene from text alone',
  'favor fast realtime synthesis and immediate visual response',
  'favor richer long-range coherence and more sustained visual continuity',
  'reinforce composition and subject consistency with reference guidance',
  'rely more on prompt styling than reference guidance',
] as const

export function getQualityPresetForValue(value: number) {
  if (value < 34) {
    return denoisingPresets[0]
  }
  if (value < 67) {
    return denoisingPresets[1]
  }
  return denoisingPresets[2]
}

export function getReferenceScaleForValue(value: number) {
  const mapped = 0.25 + (1.55 * (value / 100))
  return Math.round(mapped * 20) / 20
}

export function getNoiseScaleForStability(value: number) {
  const mapped = 0.9 - (0.6 * (value / 100))
  return Math.round(mapped * 100) / 100
}

function clampMacroValue(value: number) {
  return Math.max(0, Math.min(100, Math.round(value)))
}

function stripKnownLookPhrases(text: string) {
  let cleaned = text.trim()

  for (const phrase of lookContextPhrases) {
    const escaped = phrase.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const regex = new RegExp(`(?:,\\s*)?${escaped}`, 'gi')
    cleaned = cleaned.replace(regex, '')
  }

  return cleaned
    .replace(/\s+,/g, ',')
    .replace(/,\s*,/g, ', ')
    .replace(/^,\s*/g, '')
    .replace(/,\s*$/g, '')
    .replace(/\s{2,}/g, ' ')
    .trim()
}

export function getAdaptedLookPreset(
  lookPresetId: string,
  context: LookContext,
): AdaptedLookPreset | null {
  const preset = lookPresetDefinitions.find((entry) => entry.id === lookPresetId)
  if (!preset) {
    return null
  }

  const { mode, pipelineId, vaceEnabled } = context
  const macros = { ...preset.macros }
  const suffixParts = [preset.suffix]

  if (mode === 'webcam') {
    macros.stability += 16
    macros.quality -= 8
    macros.reference += vaceEnabled ? 12 : -6
    suffixParts.push('preserve the webcam scene structure, identity, and spatial continuity')
  } else {
    suffixParts.push('generate a complete cinematic scene from text alone')
  }

  if (pipelineId === 'streamdiffusionv2') {
    macros.quality -= 12
    macros.stability += 8
    suffixParts.push('favor fast realtime synthesis and immediate visual response')
  } else if (pipelineId === 'longlive') {
    macros.quality += 6
    macros.stability += 4
    suffixParts.push('favor richer long-range coherence and more sustained visual continuity')
  }

  if (vaceEnabled) {
    macros.reference += 10
    suffixParts.push('reinforce composition and subject consistency with reference guidance')
  } else {
    macros.reference -= 18
    suffixParts.push('rely more on prompt styling than reference guidance')
  }

  return {
    ...preset,
    note: [
      preset.note,
      mode === 'webcam' ? 'scene-preserving' : 'scene-building',
      pipelineId === 'streamdiffusionv2' ? 'fast realtime bias' : pipelineId === 'longlive' ? 'long coherence bias' : null,
      vaceEnabled ? 'VACE-guided' : 'prompt-only guidance',
    ].filter(Boolean).join(' · '),
    recommendedPipeline:
      preset.recommendedPipelines?.[mode] ||
      preset.recommendedPipelines?.receive ||
      pipelineId,
    transition: preset.transition,
    macros: {
      quality: clampMacroValue(macros.quality),
      reference: clampMacroValue(macros.reference),
      stability: clampMacroValue(macros.stability),
    },
    suffix: suffixParts.join(', '),
  }
}

export function buildPromptWithLook(
  basePrompt: string,
  adaptedPreset: AdaptedLookPreset | null,
) {
  if (!adaptedPreset) {
    return basePrompt
  }

  const trimmed = stripKnownLookPhrases(basePrompt)
  if (!trimmed) {
    return adaptedPreset.suffix
  }

  if (trimmed.toLowerCase().includes(adaptedPreset.suffix.toLowerCase())) {
    return trimmed
  }

  return `${trimmed}, ${adaptedPreset.suffix}`
}
