import React, { useState, useEffect } from 'react';

const ThreeDotLoader = () => {
  return (
    <div className='dot-pulse-container'>
      <div className='dot-pulse' />
    </div>
  );
};

const ThreeDotsLoaderText = () => {
  const [dots, setDots] = useState('');

  useEffect(() => {
    const interval = setInterval(() => {
      setDots((prevDots) => {
        if (prevDots === '...') {
          return '';
        }
        return prevDots + '.';
      });
    }, 500);

    return () => clearInterval(interval);
  }, []);

  return <span className='text-dot-loader'>{dots}</span>;
};

export default ThreeDotLoader;
export { ThreeDotsLoaderText };
