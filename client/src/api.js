export class NodeAPI {
  constructor(baseUrl) {
    // Strip trailing slashes
    this.baseUrl = baseUrl.replace(/\/$/, '');
  }

  async health() {
    const res = await fetch(`${this.baseUrl}/health`);
    if (!res.ok) throw new Error('Health check failed');
    return res.json();
  }

  async presence() {
    const res = await fetch(`${this.baseUrl}/users`);
    if (!res.ok) throw new Error('Failed to fetch presence');
    return res.json();
  }

  async setPresence(userId, status = 'online') {
    const res = await fetch(`${this.baseUrl}/presence`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_id: userId, status })
    });
    if (!res.ok) throw new Error('Failed to set presence');
    return res.json();
  }

  async getMessages(userId) {
    const res = await fetch(`${this.baseUrl}/messages/${userId}`);
    if (!res.ok) throw new Error('Failed to fetch messages');
    const data = await res.json();
    return data.messages || [];
  }

  async sendMessage(from, to, body) {
    const res = await fetch(`${this.baseUrl}/message`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ from, to, body })
    });
    if (!res.ok) throw new Error('Failed to send message');
    return res.json();
  }

  // Call Signalling
  async createCall(from, to) {
    const res = await fetch(`${this.baseUrl}/call`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ from, to })
    });
    if (!res.ok) throw new Error('Failed to create call');
    return res.json();
  }

  async updateCallState(callId, state) {
    const res = await fetch(`${this.baseUrl}/calls/${callId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ accepted: state === 'accepted' })
    });
    if (!res.ok) throw new Error('Failed to update call state');
    return res.json();
  }

  async getCalls() {
    const res = await fetch(`${this.baseUrl}/calls`);
    if (!res.ok) throw new Error('Failed to fetch calls');
    return res.json();
  }

  async getCall(callId) {
    const res = await fetch(`${this.baseUrl}/call/${callId}`);
    if (!res.ok) throw new Error('Failed to fetch call state');
    return res.json();
  }
}
