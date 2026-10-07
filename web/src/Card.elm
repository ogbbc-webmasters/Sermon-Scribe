module Card exposing (view)

import Html exposing (Html, div)
import Ui


{-| A shared content card with a title and top-right icon actions.
-}
view : Html msg -> List (Html msg) -> List (Html msg) -> Html msg
view heading actions content =
    div [ Ui.panel ]
        [ div [ Ui.panelHeader ]
            [ div [ Ui.panelHeading ] [ heading ]
            , div [ Ui.panelActions ] actions
            ]
        , div [] content
        ]
