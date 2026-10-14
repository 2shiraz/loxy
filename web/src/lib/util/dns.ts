export { relTime } from './format';
import { serviceTitle } from './services';
export function serviceLabel(s: { protocol: string; listen_port: number; destination_port: number }) { return serviceTitle(s); }

export const RECORD_HINTS: Record<string, { placeholder: string; hint: string }> = {
  A: { placeholder: '203.0.113.10', hint: 'IPv4 address' },
  AAAA: { placeholder: '2001:db8::10', hint: 'IPv6 address' },
  CNAME: { placeholder: 'target.example.com.', hint: 'Hostname this name is an alias of. Cannot coexist with other records of the same name.' },
  TXT: { placeholder: '"v=spf1 include:example.com ~all"', hint: 'Text, up to 255 characters per string; longer values are split.' },
  MX: { placeholder: '10 mail.example.com.', hint: 'priority (0–65535) and mail server hostname' },
  CAA: { placeholder: '0 issue "letsencrypt.org"', hint: 'flags tag "value" — tag is issue, issuewild or iodef' },
  SRV: { placeholder: '10 5 443 svc.example.com.', hint: 'priority weight port target' },
  NS: { placeholder: 'ns1.example.com.', hint: 'Name server hostname' }
};
