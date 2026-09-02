import { render, screen, fireEvent, act } from '@testing-library/react';
import DownloadButton from '@shared/buttons/DownloadButton';

const mockSaveAs = jest.fn();
jest.mock('file-saver', () => ({ saveAs: (...args) => mockSaveAs(...args) }));

jest.mock('@ui/Button', () => ({
  Button: ({ 'aria-label': ariaLabel, onClick, disabled, id }) => (
    <button aria-label={ariaLabel} onClick={onClick} disabled={disabled} id={id} data-testid='download-btn' />
  ),
}));

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => (
    <div data-testid='tooltip' data-title={title}>
      {children}
    </div>
  ),
}));

beforeEach(() => {
  mockSaveAs.mockClear();
});

describe('DownloadButton', () => {
  describe('rendering', () => {
    it('renders with aria-label=Download', () => {
      render(<DownloadButton />);
      expect(screen.getByLabelText('Download')).toBeInTheDocument();
    });

    it('is disabled when no onClick is provided', () => {
      render(<DownloadButton />);
      expect(screen.getByTestId('download-btn')).toBeDisabled();
    });

    it('is enabled when onClick is provided', () => {
      render(<DownloadButton onClick={jest.fn().mockResolvedValue({})} />);
      expect(screen.getByTestId('download-btn')).not.toBeDisabled();
    });

    it('shows "Coming Soon" tooltip when no onClick', () => {
      render(<DownloadButton />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', 'Coming Soon');
    });

    it('shows "Download" tooltip when onClick is provided', () => {
      render(<DownloadButton onClick={jest.fn().mockResolvedValue({})} />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', 'Download');
    });

    it('applies id prop', () => {
      render(<DownloadButton id='dl-btn' onClick={jest.fn().mockResolvedValue({})} />);
      expect(screen.getByTestId('download-btn')).toHaveAttribute('id', 'dl-btn');
    });
  });

  describe('download with data', () => {
    it('calls saveAs with a blob when onClick returns data', async () => {
      const onClick = jest.fn().mockResolvedValue({ data: 'content', fileName: 'file.txt', fileType: 'text/plain' });
      render(<DownloadButton onClick={onClick} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('download-btn'));
      });
      expect(mockSaveAs).toHaveBeenCalledWith(expect.any(Blob), 'file.txt');
    });

    it('calls saveAs with CSV blob when onClick returns table data', async () => {
      const onClick = jest.fn().mockResolvedValue({
        table: { header: [['Name', 'Age']], data: [['Alice', '30']] },
        fileName: 'data.csv',
      });
      render(<DownloadButton onClick={onClick} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('download-btn'));
      });
      expect(mockSaveAs).toHaveBeenCalledWith(expect.any(Blob), 'data.csv');
    });
  });

  describe('canvas download (single canvasId)', () => {
    let testCanvas;

    beforeEach(() => {
      testCanvas = document.createElement('canvas');
      testCanvas.id = 'my-chart';
      testCanvas.width = 200;
      testCanvas.height = 100;
      document.body.appendChild(testCanvas);
      jest.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
        fillStyle: '',
        fillRect: jest.fn(),
        drawImage: jest.fn(),
      });
      jest.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(function (callback) {
        callback(new Blob(['png'], { type: 'image/png' }));
      });
    });

    afterEach(() => {
      testCanvas.remove();
      jest.restoreAllMocks();
    });

    it('calls saveAs with a PNG blob for a single canvasId', async () => {
      const onClick = jest.fn().mockResolvedValue({ canvasId: 'my-chart', fileName: 'chart.png' });
      render(<DownloadButton onClick={onClick} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('download-btn'));
      });
      expect(mockSaveAs).toHaveBeenCalledWith(expect.any(Blob), 'chart.png');
    });

    it('defaults filename to "data.png" when no fileName given', async () => {
      const onClick = jest.fn().mockResolvedValue({ canvasId: 'my-chart' });
      render(<DownloadButton onClick={onClick} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('download-btn'));
      });
      expect(mockSaveAs).toHaveBeenCalledWith(expect.any(Blob), 'data.png');
    });
  });

  describe('canvas download (array canvasId)', () => {
    let canvas1, canvas2;

    beforeEach(() => {
      canvas1 = document.createElement('canvas');
      canvas1.id = 'chart-1';
      canvas1.width = 300;
      canvas1.height = 150;
      document.body.appendChild(canvas1);

      canvas2 = document.createElement('canvas');
      canvas2.id = 'chart-2';
      canvas2.width = 200;
      canvas2.height = 100;
      document.body.appendChild(canvas2);

      jest.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
        fillStyle: '',
        fillRect: jest.fn(),
        drawImage: jest.fn(),
      });
      jest.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(function (callback) {
        callback(new Blob(['png'], { type: 'image/png' }));
      });
    });

    afterEach(() => {
      canvas1.remove();
      canvas2.remove();
      jest.restoreAllMocks();
    });

    it('calls saveAs once with a merged PNG blob', async () => {
      const onClick = jest.fn().mockResolvedValue({
        canvasId: ['chart-1', 'chart-2'],
        fileName: 'merged.png',
      });
      render(<DownloadButton onClick={onClick} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('download-btn'));
      });
      expect(mockSaveAs).toHaveBeenCalledTimes(1);
      expect(mockSaveAs).toHaveBeenCalledWith(expect.any(Blob), 'merged.png');
    });

    it('defaults filename to "data.png" when no fileName given for array canvasId', async () => {
      const onClick = jest.fn().mockResolvedValue({ canvasId: ['chart-1', 'chart-2'] });
      render(<DownloadButton onClick={onClick} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('download-btn'));
      });
      expect(mockSaveAs).toHaveBeenCalledWith(expect.any(Blob), 'data.png');
    });

    it('uses the widest canvas width and summed height for the merged canvas', async () => {
      const createdCanvases = [];
      const origCreate = document.createElement.bind(document);
      jest.spyOn(document, 'createElement').mockImplementation((tag) => {
        const el = origCreate(tag);
        if (tag === 'canvas') createdCanvases.push(el);
        return el;
      });

      const onClick = jest.fn().mockResolvedValue({ canvasId: ['chart-1', 'chart-2'] });
      render(<DownloadButton onClick={onClick} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('download-btn'));
      });

      const mergedCanvas = createdCanvases.find((c) => c.id === 'canvasForDownload');
      expect(mergedCanvas).toBeDefined();
      expect(mergedCanvas.width).toBe(300); // max(300, 200)
      expect(mergedCanvas.height).toBe(250); // 150 + 100
    });

    it('skips non-existent canvas IDs without throwing', async () => {
      const onClick = jest.fn().mockResolvedValue({
        canvasId: ['chart-1', 'does-not-exist'],
        fileName: 'partial.png',
      });
      render(<DownloadButton onClick={onClick} />);
      await act(async () => {
        fireEvent.click(screen.getByTestId('download-btn'));
      });
      expect(mockSaveAs).toHaveBeenCalledWith(expect.any(Blob), 'partial.png');
    });
  });
});
