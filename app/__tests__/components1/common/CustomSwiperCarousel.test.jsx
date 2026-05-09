import React from 'react';
import { render, screen } from '@testing-library/react';
import CustomSwiperCarousel from '@components1/common/CustomSwiperCarousel';

jest.mock('swiper/react', () => ({
  Swiper: ({ children }) => <div data-testid='swiper'>{children}</div>,
  SwiperSlide: ({ children }) => <div data-testid='swiper-slide'>{children}</div>,
}));

jest.mock(
  'swiper/modules',
  () => ({
    Navigation: {},
    Pagination: {},
    Autoplay: {},
  }),
  { virtual: true }
);

jest.mock('react-icons/io', () => ({
  IoIosArrowBack: () => <span data-testid='arrow-back'>Back</span>,
  IoIosArrowForward: () => <span data-testid='arrow-forward'>Forward</span>,
}));

describe('CustomSwiperCarousel', () => {
  it('renders without crashing', () => {
    const { container } = render(
      <CustomSwiperCarousel>
        <div>Slide 1</div>
      </CustomSwiperCarousel>
    );
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders child slides inside swiper slides', () => {
    render(
      <CustomSwiperCarousel>
        <div>Slide A</div>
        <div>Slide B</div>
      </CustomSwiperCarousel>
    );
    expect(screen.getByText('Slide A')).toBeInTheDocument();
    expect(screen.getByText('Slide B')).toBeInTheDocument();
  });

  it('renders swiper container', () => {
    render(
      <CustomSwiperCarousel>
        <div>Content</div>
      </CustomSwiperCarousel>
    );
    expect(screen.getByTestId('swiper')).toBeInTheDocument();
  });

  it('renders swiper-carousel wrapper', () => {
    const { container } = render(
      <CustomSwiperCarousel>
        <div>Content</div>
      </CustomSwiperCarousel>
    );
    expect(container.querySelector('.swiper-carousel')).toBeInTheDocument();
  });

  it('renders default arrow icons when showArrows is true', () => {
    render(
      <CustomSwiperCarousel showArrows={true}>
        <div>Content</div>
      </CustomSwiperCarousel>
    );
    expect(screen.getByTestId('arrow-back')).toBeInTheDocument();
    expect(screen.getByTestId('arrow-forward')).toBeInTheDocument();
  });

  it('does not render arrows when showArrows is false', () => {
    render(
      <CustomSwiperCarousel showArrows={false}>
        <div>Content</div>
      </CustomSwiperCarousel>
    );
    expect(screen.queryByTestId('arrow-back')).not.toBeInTheDocument();
    expect(screen.queryByTestId('arrow-forward')).not.toBeInTheDocument();
  });

  it('renders custom prevArrow and nextArrow when provided', () => {
    render(
      <CustomSwiperCarousel
        showArrows={true}
        prevArrow={<span data-testid='custom-prev'>Prev</span>}
        nextArrow={<span data-testid='custom-next'>Next</span>}
      >
        <div>Content</div>
      </CustomSwiperCarousel>
    );
    expect(screen.getByTestId('custom-prev')).toBeInTheDocument();
    expect(screen.getByTestId('custom-next')).toBeInTheDocument();
  });

  it('renders pagination element when showBullets is true', () => {
    const { container } = render(
      <CustomSwiperCarousel showBullets={true}>
        <div>Content</div>
      </CustomSwiperCarousel>
    );
    expect(container.querySelector('.custom-pagination')).toBeInTheDocument();
  });

  it('does not render pagination element when showBullets is false', () => {
    const { container } = render(
      <CustomSwiperCarousel showBullets={false}>
        <div>Content</div>
      </CustomSwiperCarousel>
    );
    expect(container.querySelector('.custom-pagination')).not.toBeInTheDocument();
  });
});
