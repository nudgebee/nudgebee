// LazyLoadComponent.tsx
import React, { useEffect, useRef, useState, Suspense, type ComponentType } from 'react';
import ErrorBoundary, { InlineFallback } from '@common/ErrorBoundary';

interface LazyLoadComponentProps {
  component: () => Promise<{ default: ComponentType<any> }>;
  fallback?: React.ReactNode;
  props?: Record<string, any>;
  threshold?: number;
}

const LazyLoadComponent: React.FC<LazyLoadComponentProps> = ({ component, fallback = <div>Loading...</div>, props = {}, threshold = 0.1 }) => {
  const [isVisible, setIsVisible] = useState(false);
  const [LoadedComponent, setLoadedComponent] = useState<ComponentType<any> | null>(null);
  const [loadError, setLoadError] = useState<Error | null>(null);
  const [retryCount, setRetryCount] = useState(0);
  const ref = useRef<HTMLDivElement | null>(null);
  const requestToken = useRef(0);

  useEffect(() => {
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setIsVisible(true);
          observer.disconnect();
        }
      },
      { threshold }
    );

    if (ref.current) {
      observer.observe(ref.current);
    }

    return () => observer.disconnect();
  }, [threshold]);

  useEffect(() => {
    if (isVisible && !LoadedComponent) {
      const token = ++requestToken.current;
      component()
        .then((mod) => {
          if (token !== requestToken.current) return;
          setLoadedComponent(() => mod.default);
        })
        .catch((error: unknown) => {
          if (token !== requestToken.current) return;
          setLoadedComponent(null);
          setLoadError(error instanceof Error ? error : new Error(String(error)));
        });
    }
  }, [isVisible, LoadedComponent, component, retryCount]);

  const handleRetry = () => {
    setLoadError(null);
    setLoadedComponent(null);
    setRetryCount((c) => c + 1);
  };

  const renderContent = () => {
    if (loadError) {
      return <InlineFallback onRetry={handleRetry} />;
    }
    if (LoadedComponent) {
      return (
        <ErrorBoundary>
          <Suspense fallback={fallback}>
            <LoadedComponent {...props} />
          </Suspense>
        </ErrorBoundary>
      );
    }
    return fallback;
  };

  return <div ref={ref}>{renderContent()}</div>;
};

export default LazyLoadComponent;
