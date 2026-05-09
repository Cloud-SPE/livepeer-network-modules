export function installAdminPageStyles(): void {
  if (document.getElementById("video-gateway-admin-page-styles") !== null) {
    return;
  }
  const link = document.createElement("link");
  link.id = "video-gateway-admin-page-styles";
  link.rel = "stylesheet";
  link.href = new URL("./admin-pages.css", import.meta.url).href;
  document.head.append(link);
}
