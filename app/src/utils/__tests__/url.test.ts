import { isTemplateString, isValidUrl, isFetchableHttpUrl, isUrlFieldName, urlFieldStatus } from '../url';

describe('url utils', () => {
  describe('isTemplateString', () => {
    it('identifies Jinja/Go template patterns', () => {
      expect(isTemplateString('{{ Inputs.mcp_url }}')).toBe(true);
      expect(isTemplateString('{% if true %}http://mcp{% endif %}')).toBe(true);
      expect(isTemplateString('http://example.com')).toBe(false);
      expect(isTemplateString('')).toBe(false);
      expect(isTemplateString(null)).toBe(false);
      expect(isTemplateString(undefined)).toBe(false);
    });
  });

  describe('isValidUrl', () => {
    it('returns true for parseable URLs regardless of scheme', () => {
      expect(isValidUrl('http://mcp-svc:8080/mcp')).toBe(true);
      expect(isValidUrl('http://localhost:3000/mcp')).toBe(true);
      expect(isValidUrl('https://example.com/mcp')).toBe(true);
      expect(isValidUrl('socks5://h:1080')).toBe(true);
    });

    it('returns false for invalid URLs or non-URL text', () => {
      expect(isValidUrl('example.com')).toBe(false);
      expect(isValidUrl('http://')).toBe(false);
      expect(isValidUrl('{{ Inputs.mcp_url }}')).toBe(false);
      expect(isValidUrl('')).toBe(false);
      expect(isValidUrl(null)).toBe(false);
      expect(isValidUrl(undefined)).toBe(false);
    });
  });

  describe('isFetchableHttpUrl', () => {
    it('returns true only for valid HTTP/HTTPS URLs with non-empty hostnames', () => {
      expect(isFetchableHttpUrl('http://mcp-svc:8080/mcp')).toBe(true);
      expect(isFetchableHttpUrl('http://localhost:3000/mcp')).toBe(true);
      expect(isFetchableHttpUrl('https://mcp.example.com/mcp')).toBe(true);
    });

    it('returns false for non-HTTP schemes, invalid URLs, and templates', () => {
      expect(isFetchableHttpUrl('socks5://h:1080')).toBe(false);
      expect(isFetchableHttpUrl('example.com')).toBe(false);
      expect(isFetchableHttpUrl('{{ Inputs.mcp_url }}')).toBe(false);
      expect(isFetchableHttpUrl('')).toBe(false);
      expect(isFetchableHttpUrl(null)).toBe(false);
      expect(isFetchableHttpUrl(undefined)).toBe(false);
    });
  });

  describe('isUrlFieldName', () => {
    it('matches field names containing url or endpoint case-insensitively', () => {
      expect(isUrlFieldName('url')).toBe(true);
      expect(isUrlFieldName('mcp_url')).toBe(true);
      expect(isUrlFieldName('api_endpoint')).toBe(true);
      expect(isUrlFieldName('ENDPOINT')).toBe(true);
      expect(isUrlFieldName('proxy_url')).toBe(true);
    });

    it('returns false for non-url field names', () => {
      expect(isUrlFieldName('prompt')).toBe(false);
      expect(isUrlFieldName('message')).toBe(false);
      expect(isUrlFieldName('headers')).toBe(false);
      expect(isUrlFieldName('')).toBe(false);
      expect(isUrlFieldName(null)).toBe(false);
    });
  });

  describe('urlFieldStatus', () => {
    it('returns undefined for blank inputs and template strings', () => {
      expect(urlFieldStatus('')).toBeUndefined();
      expect(urlFieldStatus('   ')).toBeUndefined();
      expect(urlFieldStatus(null)).toBeUndefined();
      expect(urlFieldStatus(undefined)).toBeUndefined();
      expect(urlFieldStatus('{{ Inputs.mcp_url }}')).toBeUndefined();
    });

    it('returns valid for parseable URLs', () => {
      expect(urlFieldStatus('http://mcp-svc:8080/mcp')).toBe('valid');
      expect(urlFieldStatus('http://localhost:3000/mcp')).toBe('valid');
      expect(urlFieldStatus('socks5://h:1080')).toBe('valid');
    });

    it('returns invalid for malformed URLs without scheme', () => {
      expect(urlFieldStatus('example.com')).toBe('invalid');
      expect(urlFieldStatus('http://')).toBe('invalid');
      expect(urlFieldStatus('://badurl')).toBe('invalid');
    });
  });
});
