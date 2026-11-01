import { toPng } from 'html-to-image';
import { downloadNodeAsPng } from '../panelImage';

jest.mock('html-to-image', () => ({ toPng: jest.fn(() => Promise.resolve('data:image/png;base64,')), getFontEmbedCSS: jest.fn() }));

describe('downloadNodeAsPng', () => {
  it('passes the resolved background colour, not a CSS variable the canvas cannot read', async () => {
    const node = document.createElement('div');
    // Set on the node: jsdom's getComputedStyle does not inherit custom properties.
    node.style.setProperty('--ds-background-100', '#ffffff');
    document.body.appendChild(node);
    jest.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
      cb(0);
      return 0;
    });

    await downloadNodeAsPng(node, 'Ready nodes', 'panel');

    const { backgroundColor } = (toPng as jest.Mock).mock.calls[0][1];
    expect(backgroundColor).not.toMatch(/var\(/);
    expect(backgroundColor).toBe('#ffffff');
  });
});
