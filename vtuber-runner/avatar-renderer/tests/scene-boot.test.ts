import { describe, expect, it } from "vitest";

import { bootScene } from "../src/scene/boot";

describe("bootScene", () => {
  it("returns a SceneHandle whose mutators are no-ops on the placeholder scene", async () => {
    const canvas = document.createElement("canvas");
    canvas.width = 320;
    canvas.height = 240;

    const scene = await bootScene({
      canvas,
      vrmUrl: "",
      width: 320,
      height: 240,
    });

    expect(typeof scene.setExpression).toBe("function");
    expect(typeof scene.setLookAt).toBe("function");
    expect(typeof scene.setSpeaking).toBe("function");
    expect(typeof scene.tick).toBe("function");

    scene.setExpression("happy", 1);
    scene.setLookAt(0, 0, 1);
    scene.setSpeaking(true);
    scene.tick(0.016);
  });
});
