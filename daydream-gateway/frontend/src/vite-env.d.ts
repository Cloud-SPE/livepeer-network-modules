/// <reference types="vite/client" />

declare module '*.css'

interface Window {
  __pc?: RTCPeerConnection
}
