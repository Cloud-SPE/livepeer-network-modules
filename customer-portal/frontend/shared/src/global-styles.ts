const GLOBAL_STYLE_ID = "livepeer-network-ui-global-styles";
const GLOBAL_STYLE_HREF = new URL("./css/global.css", import.meta.url).href;

export function installGlobalStyles(): void {
  if (typeof document === "undefined") {
    return;
  }
  if (document.getElementById(GLOBAL_STYLE_ID) !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = GLOBAL_STYLE_ID;
  link.rel = "stylesheet";
  link.href = GLOBAL_STYLE_HREF;
  document.head.append(link);
}
