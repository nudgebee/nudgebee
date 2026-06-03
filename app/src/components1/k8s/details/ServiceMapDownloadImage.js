import React from 'react';
import { Panel, useReactFlow, getRectOfNodes, getTransformForBounds } from 'reactflow';
import { toPng } from 'html-to-image';
import { colors } from 'src/utils/colors';

function downloadImage(dataUrl) {
  const a = document.createElement('a');
  a.setAttribute('download', 'servicemap.png');
  a.setAttribute('href', dataUrl);
  a.click();
}

function ServiceMapDownloadImage() {
  const { getNodes } = useReactFlow();

  const onClick = () => {
    const nodesBounds = getRectOfNodes(getNodes());

    // Add padding around the graph
    const padding = 100;

    // Calculate dimensions based on actual node bounds
    const imageWidth = nodesBounds.width + padding * 2;
    const imageHeight = nodesBounds.height + padding * 2;

    // Calculate transform to center the graph with padding
    const transform = getTransformForBounds(
      nodesBounds,
      imageWidth,
      imageHeight,
      0.5, // min zoom
      2, // max zoom
      padding // padding
    );

    const viewport = document.querySelector('.react-flow__viewport');

    toPng(viewport, {
      backgroundColor: colors.background.white,
      width: imageWidth,
      height: imageHeight,
      style: {
        width: `${imageWidth}px`,
        height: `${imageHeight}px`,
        transform: `translate(${transform[0]}px, ${transform[1]}px) scale(${transform[2]})`,
      },
      // Increase quality for better output
      pixelRatio: 2,
    })
      .then(downloadImage)
      .catch((error) => {
        console.error('Error generating image:', error);
      });
  };

  return (
    <Panel position='top-right'>
      <button className='download-btn' onClick={onClick}>
        Download Image
      </button>
    </Panel>
  );
}

export default ServiceMapDownloadImage;
