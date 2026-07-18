module DateFormat exposing (formatDate)

import Iso8601
import Time


formatDate : Time.Zone -> String -> String
formatDate zone iso =
    case Iso8601.toTime iso of
        Ok posix ->
            monthName (Time.toMonth zone posix)
                ++ " "
                ++ String.fromInt (Time.toDay zone posix)
                ++ ", "
                ++ String.fromInt (Time.toYear zone posix)

        Err _ ->
            iso


monthName : Time.Month -> String
monthName month =
    case month of
        Time.Jan ->
            "January"

        Time.Feb ->
            "February"

        Time.Mar ->
            "March"

        Time.Apr ->
            "April"

        Time.May ->
            "May"

        Time.Jun ->
            "June"

        Time.Jul ->
            "July"

        Time.Aug ->
            "August"

        Time.Sep ->
            "September"

        Time.Oct ->
            "October"

        Time.Nov ->
            "November"

        Time.Dec ->
            "December"
