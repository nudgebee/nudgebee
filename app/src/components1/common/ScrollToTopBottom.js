import React, { useState, useEffect } from 'react';
import { FaArrowUp, FaArrowDown } from 'react-icons/fa';

const ScrollToTopBottom = ({ alwaysShowBottomArrow = false }) => {
  const [isVisible, setIsVisible] = useState(false);
  const [isAtBottom, setIsAtBottom] = useState(false);

  const toggleVisibility = () => {
    const scrolled = window.scrollY;
    const docHeight = document.documentElement.scrollHeight;
    const winHeight = window.innerHeight;
    if (scrolled > 250) {
      setIsVisible(true);
    } else {
      setIsVisible(false);
    }
    if (scrolled + winHeight >= docHeight - 300) {
      setIsAtBottom(true);
    } else {
      setIsAtBottom(false);
    }
  };

  const scrollToTop = () => {
    window.scrollTo({
      top: 0,
      behavior: 'smooth',
    });
  };

  const scrollToBottom = () => {
    window.scrollTo({
      top: document.documentElement.scrollHeight,
      behavior: 'smooth',
    });
  };

  useEffect(() => {
    window.addEventListener('scroll', toggleVisibility);
    return () => window.removeEventListener('scroll', toggleVisibility);
  }, []);

  const getButtonStyle = (isBottom) => {
    if (isBottom && isAtBottom) {
      return { ...styles.buttonWrapper, ...styles.moveIntoUp };
    }
    if (isVisible || (isBottom && alwaysShowBottomArrow)) {
      return { ...styles.buttonWrapper, ...styles.visible };
    }
    return { ...styles.buttonWrapper, ...styles.hidden };
  };

  return (
    <div style={styles.container}>
      <div style={getButtonStyle(false)}>
        <button onClick={scrollToTop} style={styles.button} aria-label='Scroll to top'>
          <div style={styles.circle}>
            <FaArrowUp style={styles.icon} />
          </div>
        </button>
      </div>
      <div
        style={{
          ...getButtonStyle(true),
          pointerEvents: isAtBottom ? 'none' : 'auto',
        }}
      >
        <button onClick={scrollToBottom} style={styles.button} aria-label='Scroll to bottom'>
          <div style={styles.circle}>
            <FaArrowDown style={styles.icon} />
          </div>
        </button>
      </div>
    </div>
  );
};

const styles = {
  container: {
    position: 'fixed',
    bottom: '50vh',
    right: '5vw',
    display: 'flex',
    flexDirection: 'column',
    gap: '12px',
    zIndex: 1000,
  },
  buttonWrapper: {
    transition: 'transform 0.3s ease-out, opacity 0.3s ease-out',
  },
  visible: {
    transform: 'translateX(0)',
    opacity: 1,
  },
  hidden: {
    transform: 'translateX(100%)',
    opacity: 0,
  },
  moveIntoUp: {
    transform: 'translate(0, -60px)',
    opacity: 0,
    transition: 'transform 0.3s ease-in, opacity 0.4s ease-in',
    zIndex: -1,
  },
  button: {
    backgroundColor: 'transparent',
    border: 'none',
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    cursor: 'pointer',
  },
  circle: {
    backgroundColor: '#ffffff',
    border: '1px solid #7db8ff',
    borderRadius: '50%',
    width: '40px',
    height: '40px',
    display: 'flex',
    justifyContent: 'center',
    alignItems: 'center',
  },
  icon: {
    color: '#3b82f6',
    fontSize: '16px',
  },
};

import PropTypes from 'prop-types';

ScrollToTopBottom.propTypes = {
  alwaysShowBottomArrow: PropTypes.bool,
};

export default ScrollToTopBottom;
