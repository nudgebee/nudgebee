import React, { useRef } from 'react';
import { Swiper, SwiperSlide } from 'swiper/react';
import { Navigation, Pagination } from 'swiper/modules';
import PropTypes from 'prop-types';
import { IoIosArrowBack, IoIosArrowForward } from 'react-icons/io';

const CustomSwiperCarousel = ({
  children,
  slidesToShow = 1,
  showArrows = true,
  showBullets = false,
  nextArrow,
  prevArrow,
  arrowsPosition = 'inside',
  bulletStyle = {
    activeColor: '#000',
    inactiveColor: '#ccc',
    size: '10px',
  },
  breakpoints = {},
}) => {
  const prevArrowRef = useRef(null);
  const nextArrowRef = useRef(null);

  return (
    <div className='swiper-carousel'>
      <Swiper
        modules={[Navigation, Pagination]}
        spaceBetween={20}
        slidesPerView={slidesToShow}
        slidesPerGroup={slidesToShow}
        breakpoints={{
          0: {
            slidesPerView: 1,
            slidesPerGroup: 1,
          },
          768: {
            slidesPerView: 2,
            slidesPerGroup: 2,
          },
          1300: {
            slidesPerView: 3,
            slidesPerGroup: 3,
          },
          ...breakpoints,
        }}
        navigation={{
          prevEl: prevArrowRef.current,
          nextEl: nextArrowRef.current,
        }}
        pagination={{
          clickable: true,
          el: '.custom-pagination',
          bulletClass: 'custom-bullet',
          bulletActiveClass: 'active',
        }}
        onSwiper={(swiper) => {
          swiper.params.navigation.prevEl = prevArrowRef.current;
          swiper.params.navigation.nextEl = nextArrowRef.current;
          swiper.navigation.init();
          swiper.navigation.update();
        }}
      >
        {React.Children.map(children, (child, index) => (
          <SwiperSlide key={index}>{child}</SwiperSlide>
        ))}
      </Swiper>

      {showArrows && (
        <>
          <div ref={prevArrowRef} className={`custom-prev-arrow ${arrowsPosition}`}>
            {prevArrow || <IoIosArrowBack />}
          </div>
          <div ref={nextArrowRef} className={`custom-next-arrow ${arrowsPosition}`}>
            {nextArrow || <IoIosArrowForward />}
          </div>
        </>
      )}

      {showBullets && (
        <div
          className='custom-pagination'
          style={{
            '--active-color': bulletStyle.activeColor,
            '--inactive-color': bulletStyle.inactiveColor,
            '--bullet-size': bulletStyle.size,
          }}
        />
      )}
    </div>
  );
};

CustomSwiperCarousel.propTypes = {
  children: PropTypes.node,
  slidesToShow: PropTypes.number,
  showArrows: PropTypes.bool,
  showBullets: PropTypes.bool,
  nextArrow: PropTypes.any,
  prevArrow: PropTypes.any,
  arrowsPosition: PropTypes.string,
  bulletStyle: PropTypes.shape({
    activeColor: PropTypes.string,
    inactiveColor: PropTypes.string,
    size: PropTypes.string,
  }),
  breakpoints: PropTypes.object,
};

export default CustomSwiperCarousel;
