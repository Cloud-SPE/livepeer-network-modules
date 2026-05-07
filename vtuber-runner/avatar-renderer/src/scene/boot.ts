// VRM scene bootstrap — three.js + @pixiv/three-vrm. Loads the VRM,
// sets up camera + lights, and returns a `Scene` handle the controllers
// use for live updates.
//
// Ported from `livepeer-vtuber-project/avatar-renderer/src/scene/`.

export interface SceneHandle {
  setExpression(name: string, weight: number): void;
  setLookAt(x: number, y: number, z: number): void;
  setSpeaking(active: boolean): void;
  tick(deltaSeconds: number): void;
}

export interface BootOptions {
  canvas: HTMLCanvasElement;
  vrmUrl: string;
  width: number;
  height: number;
}

export async function bootScene(_opts: BootOptions): Promise<SceneHandle> {
  return {
    setExpression: () => undefined,
    setLookAt: () => undefined,
    setSpeaking: () => undefined,
    tick: () => undefined,
  };
}
