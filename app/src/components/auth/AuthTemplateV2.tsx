import * as React from 'react';
import Box from '@mui/material/Box';
import Grid from '@mui/material/Grid';
import Typography from '@mui/material/Typography';
import { useMediaQuery } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { SignInInvestigate, SignInOps, SignInWorkflow, SignInOptimize } from '@assets';
import { useBrandingConfig } from '@hooks/useTenantBranding';

// A carousel slide. `image` is a bundled static import (default slides) or a
// partner-supplied URL string sourced from branding config (theme.json).
export interface CarouselSlide {
  title: string;
  // A partner-supplied URL string, or a bundled static image import
  // (`StaticImageData`, or the module namespace from `require('*.png')`).
  image: string | { src: string } | { default: { src: string } };
}

// Feature carousel slide data — bundled defaults, used when a partner hasn't
// supplied its own `carouselSlides` in branding config.
export const carouselSlides: CarouselSlide[] = [
  {
    title: 'AI SRE Troubleshooting',
    image: SignInInvestigate,
  },
  {
    title: 'CloudOps AI-Assistant',
    image: SignInOps,
  },
  {
    title: 'Build your own Automation',
    image: SignInWorkflow,
  },
  {
    title: 'Continuous real-time optimization',
    image: SignInOptimize,
  },
];

interface FeatureCarouselProps {
  slides: CarouselSlide[];
}

// Feature Carousel Component
export const FeatureCarousel: React.FC<FeatureCarouselProps> = ({ slides }) => {
  const [activeIndex, setActiveIndex] = React.useState(0);
  const [isPaused, setIsPaused] = React.useState(false);
  const intervalRef = React.useRef<NodeJS.Timeout | null>(null);

  // Auto-rotate every 4 seconds
  React.useEffect(() => {
    if (isPaused) {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      return;
    }

    intervalRef.current = setInterval(() => {
      setActiveIndex((prev) => (prev + 1) % slides.length);
    }, 4000);

    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
    };
  }, [slides.length, isPaused]);

  return (
    <Box
      sx={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        marginTop: { md: '40px', lg: '50px', xl: '60px' },
        alignItems: 'center',
        padding: { md: '20px', lg: '30px', xl: '40px' },
        position: 'relative',
      }}
      onMouseEnter={() => setIsPaused(true)}
      onMouseLeave={() => setIsPaused(false)}
    >
      {/* Carousel with Title and Image */}
      <Box
        sx={{
          position: 'relative',
          width: '100%',
          maxWidth: { md: '500px', lg: '600px', xl: '710px' },
        }}
      >
        {slides.map((slide, index) => (
          <Box
            key={index}
            sx={{
              position: index === 0 ? 'relative' : 'absolute',
              top: 0,
              left: 0,
              right: 0,
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              width: '100%',
              opacity: activeIndex === index ? 1 : 0,
              transform: activeIndex === index ? 'translateY(0)' : 'translateY(10px)',
              transition: 'opacity 0.6s ease-in-out, transform 0.6s ease-in-out',
              pointerEvents: activeIndex === index ? 'auto' : 'none',
            }}
          >
            {/* Slide Title */}
            <Typography
              sx={{
                fontSize: { md: '18px', lg: '21px', xl: '24px' },
                fontWeight: 500,
                color: '#1a365d',
                textAlign: 'center',
                mb: { md: 1.5, lg: 2 },
                fontFamily: 'Poppins, sans-serif',
                letterSpacing: '-1px',
              }}
            >
              {slide.title}
            </Typography>

            {/* Slide Image */}
            <Box
              sx={{
                width: '100%',
                borderRadius: { md: '12px', lg: '14px', xl: '16px' },
                overflow: 'hidden',
                boxShadow: '0 8px 20px rgba(0, 0, 0, 0.14)',
              }}
            >
              <SafeIcon
                src={slide.image}
                alt={slide.title}
                style={{
                  width: '100%',
                  height: 'auto',
                  display: 'block',
                }}
              />
            </Box>
          </Box>
        ))}
      </Box>

      {/* Navigation Dots */}
      <Box sx={{ display: 'flex', gap: { md: 0.75, lg: 1 }, mt: { md: 2, lg: 2.5, xl: 3 } }}>
        {slides.map((_, index) => (
          <Box
            key={index}
            onClick={() => setActiveIndex(index)}
            sx={{
              width: activeIndex === index ? { md: '20px', lg: '22px', xl: '24px' } : { md: '6px', lg: '7px', xl: '8px' },
              height: { md: '6px', lg: '7px', xl: '8px' },
              borderRadius: '4px',
              backgroundColor: activeIndex === index ? '#1a365d' : 'rgba(26, 54, 93, 0.3)',
              cursor: 'pointer',
              transition: 'all 0.3s ease',
              '&:hover': {
                backgroundColor: activeIndex === index ? '#1a365d' : 'rgba(26, 54, 93, 0.5)',
              },
            }}
          />
        ))}
      </Box>

      {/* Bottom Statistics Bar */}
      <Box
        sx={{
          position: 'absolute',
          bottom: { md: '80px', lg: '120px', xl: '160px' },
          left: { md: '20px', lg: '30px', xl: '40px' },
          right: { md: '20px', lg: '30px', xl: '40px' },
          display: 'flex',
          justifyContent: 'space-around',
          borderTop: '1px solid rgba(26, 54, 93, 0.2)',
          paddingTop: { md: '12px', lg: '14px', xl: '16px' },
        }}
      >
        {[
          { value: '30+', label: 'agents' },
          { value: '50+', label: 'tools' },
          { value: '20+', label: 'integrations' },
          { value: '24/7', label: 'monitoring' },
        ].map((stat, index) => (
          <Box key={index} sx={{ display: 'flex', flexDirection: 'row', alignItems: 'flex-end', gap: { md: '2px', lg: '3px', xl: '4px' } }}>
            <Typography
              sx={{
                fontSize: { md: '16px', lg: '19px', xl: '22px' },
                fontWeight: 700,
                color: '#1a365d',
                lineHeight: { md: '22px', lg: '25px', xl: '28px' },
                fontFamily: 'Roboto, sans-serif',
              }}
            >
              {stat.value}
            </Typography>
            <Typography
              sx={{
                fontSize: { md: '14px', lg: '16px', xl: '18px' },
                lineHeight: { md: '20px', lg: '22px', xl: '24px' },
                color: 'rgba(26, 54, 93, 0.7)',
                fontFamily: 'Roboto, sans-serif',
              }}
            >
              {stat.label}
            </Typography>
          </Box>
        ))}
      </Box>
    </Box>
  );
};

interface AuthTemplateV2Props {
  children: React.ReactNode;
}

// Main Auth Template Component with two-column layout
export const AuthTemplateV2: React.FC<AuthTemplateV2Props> = ({ children }) => {
  const mobileView = useMediaQuery('(max-width:900px)');
  const brandingConfig = useBrandingConfig();
  const [signinLeftImageFailed, setSigninLeftImageFailed] = React.useState(false);
  const signinLeftImageUrl = !brandingConfig?.loading && !signinLeftImageFailed ? brandingConfig?.signinLeftImageUrl : '';

  // Prefer partner-supplied carousel slides from branding config; fall back to bundled defaults.
  const partnerSlides = brandingConfig?.carouselSlides as CarouselSlide[] | null | undefined;
  const slides: CarouselSlide[] = partnerSlides && partnerSlides.length > 0 ? partnerSlides : carouselSlides;

  return (
    <Grid container sx={{ height: '100vh', overflow: 'hidden' }}>
      {/* Left Column - Feature Carousel or custom image (60%) - Fixed, no scroll */}
      {!mobileView && (
        <Grid
          item
          md={7}
          sx={{
            background: signinLeftImageUrl ? '#FFFFFF' : 'linear-gradient(135deg, #EFF6FF 0%, #DBEAFE 50%, #E0F2FE 100%)',
            height: '100vh',
            position: 'relative',
            overflow: 'hidden',
          }}
        >
          {signinLeftImageUrl ? (
            <Box sx={{ width: '100%', height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={signinLeftImageUrl}
                alt='Sign In'
                style={{ maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }}
                onError={() => setSigninLeftImageFailed(true)}
              />
            </Box>
          ) : (
            <>
              <FeatureCarousel slides={slides} />
            </>
          )}
        </Grid>
      )}

      {/* Right Column - Form Content (40%) - Scrollable */}
      <Grid
        item
        xs={12}
        md={5}
        sx={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: { xs: 'flex-start', md: 'center' },
          alignItems: 'center',
          padding: { xs: '24px', md: '60px 80px' },
          backgroundColor: '#FFFFFF',
          height: '100vh',
          overflowY: 'auto',
        }}
      >
        <Box
          sx={{
            width: '100%',
            maxWidth: '400px',
          }}
        >
          {children}
        </Box>
      </Grid>
    </Grid>
  );
};

export default AuthTemplateV2;
