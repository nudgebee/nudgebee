import React, { useState, useEffect, useRef } from 'react';
import Image from 'next/image';
import PropTypes from 'prop-types';

const SafeIcon = ({ src, alt = 'icon', ...props }) => {
  // Extract fallbackSrc: either from { src, fallbackSrc } object or from props
  let iconInput = src;
  let fallback = props.fallbackSrc || null;
  if (src && typeof src === 'object' && src.fallbackSrc && typeof src.src === 'string') {
    iconInput = src.src;
    fallback = fallback || src.fallbackSrc;
  }
  const { fallbackSrc: _unusedFallback, ...restProps } = props;

  const [imgSrc, setImgSrc] = useState(null);
  const prevSrcRef = useRef(iconInput);

  useEffect(() => {
    if (prevSrcRef.current !== iconInput) {
      prevSrcRef.current = iconInput;
      setImgSrc(null);
    }
  }, [iconInput]);

  if (!iconInput && !imgSrc) {
    return null;
  }

  const activeSrc = imgSrc || iconInput;

  if (React.isValidElement(activeSrc)) {
    return activeSrc;
  }

  const iconSource = activeSrc.default || activeSrc;

  if (typeof iconSource === 'function' || (typeof iconSource === 'object' && !iconSource.src)) {
    const Icon = iconSource;
    // Destructure Next/Image specific props that might warn on DOM elements or custom components
    const { fill, ...rest } = restProps;

    const style = {
      display: 'block',
      ...rest.style,
    };

    if (fill) {
      style.width = '100%';
      style.height = '100%';
      style.position = 'absolute';
      style.top = 0;
      style.left = 0;
      style.objectFit = style.objectFit || 'contain';
    } else {
      style.maxWidth = '100%';
      style.maxHeight = '100%';
      if (rest.width) {
        style.width = typeof rest.width === 'number' ? `${rest.width}px` : rest.width;
      }
      if (rest.height) {
        style.height = typeof rest.height === 'number' ? `${rest.height}px` : rest.height;
      }
    }

    return <Icon {...rest} style={style} />;
  }

  const isSvgUrl = typeof iconSource === 'string' && iconSource.endsWith('.svg');
  return (
    <Image
      src={iconSource}
      alt={alt}
      unoptimized={isSvgUrl}
      onError={() => {
        // Use explicit fallback if provided, otherwise derive default for branding URLs
        const fallbackUrl =
          fallback ||
          (typeof iconSource === 'string' && iconSource.startsWith('/branding/') && !iconSource.startsWith('/branding/default/')
            ? iconSource.replace(/\/branding\/[^/]+\//, '/branding/default/')
            : null);
        if (fallbackUrl && iconSource !== fallbackUrl) {
          setImgSrc(fallbackUrl);
        }
      }}
      {...restProps}
    />
  );
};

SafeIcon.propTypes = {
  src: PropTypes.any,
  alt: PropTypes.string,
};

export default SafeIcon;
