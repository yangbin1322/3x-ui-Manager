import { describe, it, expect } from 'vitest';

import { NodeFormSchema } from '@/schemas/node';
import { ManagedServerFormSchema } from '@/schemas/managedServer';

function serverValues(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    name: 'ssh-box',
    address: '203.0.113.5',
    enable: true,
    allowPrivateAddress: false,
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

describe('ManagedServerFormSchema editing', () => {
  it('accepts a server with a stored password (no re-entry)', () => {
    const result = ManagedServerFormSchema.safeParse(serverValues());
    expect(result.success).toBe(true);
  });

  it('accepts editing a server that re-enters a new password', () => {
    const result = ManagedServerFormSchema.safeParse(serverValues({ sshPassword: 'newpass', sshPasswordSet: false }));
    expect(result.success).toBe(true);
  });

  it('still requires an ssh credential when none is stored or entered', () => {
    const result = ManagedServerFormSchema.safeParse(serverValues({ sshPassword: '', sshPasswordSet: false }));
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues.some((i) => i.path.includes('sshPassword'))).toBe(true);
    }
  });
});

describe('NodeFormSchema', () => {
  it('enforces the 1..65535 port range', () => {
    const apiValues = {
      id: 2, name: 'api-box', scheme: 'https', address: 'node.example.com',
      port: 0, basePath: '/', apiToken: 'tok', enable: true, allowPrivateAddress: false,
      tlsVerifyMode: 'verify',
    };
    const result = NodeFormSchema.safeParse(apiValues);
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues.some((i) => i.path.includes('port'))).toBe(true);
    }
  });
});
