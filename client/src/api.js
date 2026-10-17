export class NodeAPI {
  constructor(baseUrl) {
    const parsed = new URL(baseUrl);
    if (!['http:', 'https:'].includes(parsed.protocol)) throw new Error('Use an HTTP or HTTPS node address.');
    if (parsed.username || parsed.password || parsed.search || parsed.hash || (parsed.pathname !== '/' && parsed.pathname !== '')) {
      throw new Error('Enter a node address with its host and port only.');
    }
    this.baseUrl = parsed.toString().replace(/\/$/, '');
  }

  async request(path, options) {
    let response;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 8000);
    try {
      response = await fetch(`${this.baseUrl}${path}`, { ...options, signal: controller.signal });
    } catch (cause) {
      throw new Error(controller.signal.aborted ? 'The node did not respond in time.' : 'Could not reach the node. Check the address and local Wi-Fi connection.');
    } finally {
      clearTimeout(timeout);
    }
    if (!response.ok) {
      let message = `Node request failed (${response.status}).`;
      try { message = (await response.json()).error || message; } catch { /* Keep fallback. */ }
      throw new Error(message);
    }
    return response;
  }

  async health() {
    return (await this.request('/health')).json();
  }

  async register(username, deviceId = '') {
    const response = await this.request('/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, device_id: deviceId || undefined }),
    });
    return response.json();
  }

  openSocket(deviceId, handlers = {}) {
    const url = new URL(this.baseUrl);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    url.pathname = `${url.pathname.replace(/\/$/, '')}/ws`;
    url.search = new URLSearchParams({ device_id: deviceId }).toString();
    const socket = new WebSocket(url);
    socket.onopen = handlers.onOpen;
    socket.onclose = handlers.onClose;
    socket.onerror = handlers.onError;
    socket.onmessage = (event) => {
      try { handlers.onMessage?.(JSON.parse(event.data)); } catch (error) {
        console.error('Invalid node event', error);
      }
    };
    return socket;
  }
}
