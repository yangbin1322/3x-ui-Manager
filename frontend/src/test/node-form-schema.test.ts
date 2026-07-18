import { describe, it, expect } from 'vitest';

import { NodeFormSchema } from '@/schemas/node';

// The form fills unmounted API fields with whatever the node carries. An ssh
// node comes back from the backend with port 0 and an empty scheme (they are
// cleared on save), so the schema must accept those in ssh mode — otherwise the
// base number/enum rules fire before the mode-aware superRefine and the edit
// fails with "Invalid input".
function sshNodeValues(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    name: 'ssh-box',
    mode: 'ssh',
    scheme: 'https',
    address: '203.0.113.5',
    port: 0,
    basePath: '',
    apiToken: '',
    enable: true,
    allowPrivateAddress: false,
    tlsVerifyMode: 'verify',
    sshPort: 22,
    sshUser: 'root',
    sshAuthType: 'password',
    sshPassword: '',
    sshPrivateKey: '',
    sshKeyPassphrase: '',
    sshHostKeyMode: 'trust',
    sshHostKeySha256: '',
    sshPasswordSet: true,
    ...overrides,
  };
}

describe('NodeFormSchema ssh-mode editing', () => {
  it('accepts an ssh node with port 0 and a stored password (no re-entry)', () => {
    const result = NodeFormSchema.safeParse(sshNodeValues());
    expect(result.success).toBe(true);
  });

  it('accepts editing an ssh node that re-enters a new password', () => {
    const result = NodeFormSchema.safeParse(sshNodeValues({ sshPassword: 'newpass', sshPasswordSet: false }));
    expect(result.success).toBe(true);
  });

  it('still requires an ssh credential when none is stored or entered', () => {
    const result = NodeFormSchema.safeParse(sshNodeValues({ sshPassword: '', sshPasswordSet: false }));
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues.some((i) => i.path.includes('sshPassword'))).toBe(true);
    }
  });

  it('still enforces the 1..65535 port range for API-mode nodes', () => {
    const apiValues = {
      id: 2, name: 'api-box', mode: 'api', scheme: 'https', address: 'node.example.com',
      port: 0, basePath: '/', apiToken: 'tok', enable: true, allowPrivateAddress: false,
      tlsVerifyMode: 'verify', sshPort: 22, sshUser: 'root', sshAuthType: 'password',
      sshPassword: '', sshPrivateKey: '', sshKeyPassphrase: '', sshHostKeyMode: 'trust', sshHostKeySha256: '',
    };
    const result = NodeFormSchema.safeParse(apiValues);
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues.some((i) => i.path.includes('port'))).toBe(true);
    }
  });
});
