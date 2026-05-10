import { Capability } from "../livepeer/capabilityMap.js";

export type SurfaceFieldType =
  | "string"
  | "textarea"
  | "number"
  | "boolean"
  | "enum"
  | "json"
  | "string[]"
  | "file";

export interface SurfaceFieldOption {
  value: string;
  label: string;
}

export interface SurfaceFieldDescriptor {
  name: string;
  label: string;
  type: SurfaceFieldType;
  required: boolean;
  advanced: boolean;
  description: string;
  options?: SurfaceFieldOption[];
  modelDependent?: boolean;
}

export interface SurfaceResponseVariant {
  id: string;
  label: string;
  description: string;
}

export interface CapabilitySurfaceDescriptor {
  capability: string;
  requestTransport: "json" | "multipart" | "binary";
  requestFields: SurfaceFieldDescriptor[];
  responseVariants: SurfaceResponseVariant[];
  rawSupported: boolean;
  modelDependentFields: string[];
}

const SURFACES: Record<string, CapabilitySurfaceDescriptor> = {
  [Capability.ChatCompletions]: {
    capability: Capability.ChatCompletions,
    requestTransport: "json",
    requestFields: [
      field("model", "Model", "string", true, false, "Target model id."),
      field("messages", "Messages", "json", true, false, "Conversation message array."),
      field("stream", "Stream", "boolean", false, false, "Return SSE stream instead of one JSON response."),
      field("modalities", "Modalities", "json", false, true, "Requested output modalities when supported."),
      field("response_format", "Response format", "json", false, true, "Structured output or JSON schema configuration."),
      field("tools", "Tools", "json", false, true, "Tool definitions available to the model."),
      field("tool_choice", "Tool choice", "json", false, true, "Controls tool invocation behavior."),
      field("parallel_tool_calls", "Parallel tool calls", "boolean", false, true, "Allow multiple tool calls in one turn."),
      field("n", "Choices", "number", false, true, "Number of completions to generate."),
      field("temperature", "Temperature", "number", false, false, "Sampling temperature."),
      field("top_p", "Top p", "number", false, true, "Nucleus sampling."),
      field("max_completion_tokens", "Max completion tokens", "number", false, false, "Upper bound for generated tokens."),
      field("stop", "Stop", "json", false, true, "Stop sequences."),
      field("presence_penalty", "Presence penalty", "number", false, true, "Penalty for new topics."),
      field("frequency_penalty", "Frequency penalty", "number", false, true, "Penalty for repetition."),
      field("seed", "Seed", "number", false, true, "Seed for reproducibility where supported.", true),
      field("stream_options", "Stream options", "json", false, true, "Streaming options such as usage inclusion."),
      field("user", "User", "string", false, true, "End-user identifier."),
      field("metadata", "Metadata", "json", false, true, "Opaque metadata object."),
    ],
    responseVariants: [
      variant("chat.completion", "Completion JSON", "Standard JSON completion response."),
      variant("chat.completion.chunk", "Streaming SSE", "Incremental SSE stream with chunks and final usage."),
      variant("tool_calls", "Tool calls", "Responses may include tool call directives."),
      variant("structured_output", "Structured output", "Responses may conform to a requested schema."),
      variant("usage", "Usage", "Token accounting data when returned."),
    ],
    rawSupported: true,
    modelDependentFields: ["modalities", "response_format", "tools", "tool_choice", "parallel_tool_calls", "seed"],
  },
  [Capability.Embeddings]: {
    capability: Capability.Embeddings,
    requestTransport: "json",
    requestFields: [
      field("model", "Model", "string", true, false, "Target embedding model id."),
      field("input", "Input", "json", true, false, "One string or an array of strings."),
      field("dimensions", "Dimensions", "number", false, true, "Output dimensions when supported.", true),
      field("encoding_format", "Encoding format", "enum", false, true, "Float or base64 embedding encoding.", false, [
        { value: "float", label: "float" },
        { value: "base64", label: "base64" },
      ]),
      field("user", "User", "string", false, true, "End-user identifier."),
    ],
    responseVariants: [
      variant("embedding_list", "Embeddings list", "Embedding vectors and per-item indexes."),
      variant("usage", "Usage", "Prompt and total token usage."),
    ],
    rawSupported: true,
    modelDependentFields: ["dimensions"],
  },
  [Capability.ImagesGenerations]: {
    capability: Capability.ImagesGenerations,
    requestTransport: "json",
    requestFields: [
      field("model", "Model", "string", true, false, "Target image model id."),
      field("prompt", "Prompt", "textarea", true, false, "Natural language image prompt."),
      field("background", "Background", "enum", false, true, "Background style when supported.", true, [
        { value: "transparent", label: "transparent" },
        { value: "opaque", label: "opaque" },
        { value: "auto", label: "auto" },
      ]),
      field("moderation", "Moderation", "enum", false, true, "Moderation level when supported.", true, [
        { value: "low", label: "low" },
        { value: "auto", label: "auto" },
      ]),
      field("n", "Images", "number", false, false, "Number of images to generate."),
      field("output_compression", "Output compression", "number", false, true, "Compression level for image output.", true),
      field("output_format", "Output format", "enum", false, false, "Requested image file format.", false, [
        { value: "png", label: "png" },
        { value: "jpeg", label: "jpeg" },
        { value: "webp", label: "webp" },
      ]),
      field("quality", "Quality", "enum", false, false, "Requested quality tier.", false, [
        { value: "standard", label: "standard" },
        { value: "hd", label: "hd" },
      ]),
      field("response_format", "Response format", "enum", false, false, "How generated images are returned.", false, [
        { value: "b64_json", label: "b64_json" },
        { value: "url", label: "url" },
      ]),
      field("size", "Size", "enum", false, false, "Image dimensions.", false, [
        { value: "1024x1024", label: "1024x1024" },
        { value: "1536x1024", label: "1536x1024" },
        { value: "1024x1536", label: "1024x1536" },
      ]),
      field("stream", "Stream", "boolean", false, true, "Return image generation as a stream where supported.", true),
      field("user", "User", "string", false, true, "End-user identifier."),
    ],
    responseVariants: [
      variant("image_b64", "Base64 images", "Images returned inline as base64 JSON."),
      variant("image_url", "Image URLs", "Images returned as URLs."),
      variant("revised_prompt", "Revised prompt", "Model may revise the supplied prompt."),
    ],
    rawSupported: true,
    modelDependentFields: ["background", "moderation", "output_compression", "stream"],
  },
  [Capability.AudioSpeech]: {
    capability: Capability.AudioSpeech,
    requestTransport: "json",
    requestFields: [
      field("model", "Model", "string", true, false, "Target speech model id."),
      field("input", "Input", "textarea", true, false, "Text to synthesize."),
      field("voice", "Voice", "string", true, false, "Voice id."),
      field("instructions", "Instructions", "textarea", false, true, "Voice or style instructions.", true),
      field("response_format", "Response format", "enum", false, false, "Audio output format.", false, [
        { value: "mp3", label: "mp3" },
        { value: "wav", label: "wav" },
        { value: "opus", label: "opus" },
      ]),
      field("speed", "Speed", "number", false, true, "Playback speed."),
      field("stream_format", "Stream format", "enum", false, true, "Streaming wire format when supported.", true, [
        { value: "sse", label: "sse" },
        { value: "audio", label: "audio" },
      ]),
    ],
    responseVariants: [
      variant("audio_binary", "Audio binary", "Binary audio payload."),
      variant("audio_stream", "Audio stream", "Streaming audio output when supported."),
    ],
    rawSupported: true,
    modelDependentFields: ["instructions", "stream_format"],
  },
  [Capability.AudioTranscriptions]: {
    capability: Capability.AudioTranscriptions,
    requestTransport: "multipart",
    requestFields: [
      field("file", "Audio file", "file", true, false, "Audio file upload."),
      field("model", "Model", "string", true, false, "Target transcription model id."),
      field("language", "Language", "string", false, true, "Language hint."),
      field("prompt", "Prompt", "textarea", false, true, "Prompt to guide transcription."),
      field("response_format", "Response format", "enum", false, false, "Requested response shape.", false, [
        { value: "json", label: "json" },
        { value: "text", label: "text" },
        { value: "srt", label: "srt" },
        { value: "verbose_json", label: "verbose_json" },
        { value: "vtt", label: "vtt" },
      ]),
      field("stream", "Stream", "boolean", false, true, "Return streamed transcription events when supported.", true),
      field("temperature", "Temperature", "number", false, true, "Sampling temperature."),
      field("timestamp_granularities", "Timestamp granularities", "json", false, true, "Requested timestamp precision."),
    ],
    responseVariants: [
      variant("transcript_text", "Transcript text", "Plain transcript text output."),
      variant("transcript_verbose", "Verbose JSON", "Verbose transcript JSON with segments and timestamps."),
      variant("transcript_stream", "Streaming events", "SSE stream of incremental transcript events."),
    ],
    rawSupported: true,
    modelDependentFields: ["stream", "timestamp_granularities"],
  },
};

export function surfaceForCapability(capability: string): CapabilitySurfaceDescriptor | null {
  return SURFACES[capability] ?? null;
}

function field(
  name: string,
  label: string,
  type: SurfaceFieldType,
  required: boolean,
  advanced: boolean,
  description: string,
  modelDependent: boolean = false,
  options?: SurfaceFieldOption[],
): SurfaceFieldDescriptor {
  return { name, label, type, required, advanced, description, modelDependent, options };
}

function variant(id: string, label: string, description: string): SurfaceResponseVariant {
  return { id, label, description };
}
