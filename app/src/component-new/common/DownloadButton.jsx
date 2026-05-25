/**
 * @deprecated Use <Button tone="ghost" composition="icon-only" icon={<Download/>} aria-label="Download" /> from '@components1/ds/Button' instead.
 * Tracked for removal 2026-06-06 (30-day deprecation clock from V2 ship 2026-05-07).
 */
import CustomTooltip from '@common/CustomTooltip';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import { saveAs } from 'file-saver';
import PropTypes from 'prop-types';
import { Button } from '@components1/ds/Button';

// Sanitize a cell value for CSV: escape double quotes and replace newlines with spaces
const csvSanitize = (value) => {
  const str = value == null ? '' : String(value);
  return str.replace(/"/g, '""').replace(/[\r\n]+/g, ' ');
};

const DownloadButton = ({ id, onClick }) => {
  const onDownloadClick = async () => {
    if (onClick) {
      let options = await onClick();
      if (options.data) {
        let blob = new Blob([options.data], { type: options.fileType || 'application/octet-stream' });
        saveAs(blob, options.fileName || 'data.txt');
      } else if (options.canvasId) {
        if (Array.isArray(options.canvasId)) {
          // generate image for each id and merge them
          let canvas = document.createElement('canvas');
          canvas.id = 'canvasForDownload';
          let ctx = canvas.getContext('2d');
          let width = 0;
          let height = 0;
          for (const canvasId of options.canvasId) {
            let c = document.getElementById(canvasId);
            if (c) {
              width = Math.max(width, c.width);
              height += c.height;
            }
          }
          canvas.width = width;
          canvas.height = height;
          ctx.fillStyle = '#ffffff';
          ctx.fillRect(0, 0, canvas.width, canvas.height);
          let y = 0;
          for (const canvasId of options.canvasId) {
            let c = document.getElementById(canvasId);
            if (c) {
              ctx.drawImage(c, 0, y);
              y += c.height;
            }
          }
          canvas.toBlob(function (blob) {
            saveAs(blob, options.fileName || 'data.png');
            setTimeout(() => {
              canvas.remove();
            }, 1000);
          });
        } else {
          let srcCanvas = document.getElementById(options.canvasId);
          let canvas = document.createElement('canvas');
          canvas.width = srcCanvas.width;
          canvas.height = srcCanvas.height;
          let ctx = canvas.getContext('2d');
          ctx.fillStyle = '#ffffff';
          ctx.fillRect(0, 0, canvas.width, canvas.height);
          ctx.drawImage(srcCanvas, 0, 0);
          canvas.toBlob(function (blob) {
            saveAs(blob, options.fileName || 'data.png');
          });
        }
      } else if (options.table) {
        let csvContent = '';
        options.table.header?.forEach(function (rowArray) {
          let row = rowArray.map((r) => `"${csvSanitize(r)}"`).join(',');
          csvContent += row + '\r\n';
        });
        options.table.data?.forEach(function (rowArray) {
          let row = rowArray.map((r) => `"${csvSanitize(r)}"`).join(',');
          csvContent += row + '\r\n';
        });
        let blob = new Blob([csvContent], { type: 'text/csv;charset=utf-8' });
        saveAs(blob, options.fileName || 'data.csv');
      } else if (options.tableId) {
        let csvContent = '';
        let oTable = document.getElementById(options.tableId);

        // 1. SCAPE HEADERS (Filtered)
        const headerRows = oTable?.querySelectorAll('thead tr');
        const headerRow = headerRows?.[headerRows.length - 1];
        if (headerRow) {
          const headers = [...headerRow.children]
            .filter((th) => th.getAttribute('data-export-enabled') !== 'false') // Filter headers
            .map((th) => `"${csvSanitize(th.innerText)}"`)
            .join(',');
          csvContent += headers + '\r\n';
        }

        //get from data-export-data attribute (only tbody rows to avoid duplicating headers)
        const bodyRows = oTable?.querySelectorAll('tbody tr') || [];
        let data = [...bodyRows].map((t) =>
          [...t.children]
            .filter((u) => u.getAttribute('data-export-enabled') === 'true')
            .map((u) => {
              return u.getAttribute('data-export-data') || u.innerText;
            })
        );
        let csvData = '';
        data.forEach(function (rowArray) {
          if (rowArray.length === 0) {
            return;
          }
          let row = rowArray.map((r) => `"${csvSanitize(r)}"`).join(',');
          csvData += row + '\r\n';
        });
        let blob = new Blob([csvContent + csvData], { type: 'text/csv;charset=utf-8' });
        saveAs(blob, options.fileName || 'data.txt');
      }
    }
  };

  return (
    <CustomTooltip title={onClick ? 'Download' : 'Coming Soon'}>
      <Button
        id={id}
        tone='secondary'
        composition='icon-only'
        icon={<FileDownloadOutlinedIcon />}
        aria-label='Download'
        onClick={onDownloadClick}
        disabled={!onClick}
      />
    </CustomTooltip>
  );
};

DownloadButton.propTypes = {
  id: PropTypes.string,
  onClick: PropTypes.func,
};

export default DownloadButton;
